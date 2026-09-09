package service

import (
	"context"
	"io"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

type TokenEstimator struct {
	m               multipliers
	count           float64
	currentWordType int
}

type StreamingTokenCounter struct {
	estimator *TokenEstimator
	exact     *strings.Builder
	model     string
	bytes     int
	openAI    bool
}

func NewStreamingTokenCounter(model string) *StreamingTokenCounter {
	counter := &StreamingTokenCounter{
		estimator: NewTokenEstimator(model),
		model:     model,
	}
	if common.IsOpenAITextModel(model) {
		counter.openAI = true
		counter.exact = &strings.Builder{}
	}
	return counter
}

func (c *StreamingTokenCounter) WriteString(text string) (int, error) {
	c.bytes += len(text)
	_, _ = c.estimator.WriteString(text)
	if c.exact != nil {
		if c.exact.Len()+len(text) <= exactTokenizerTextLimit {
			return c.exact.WriteString(text)
		}
		c.exact = nil
	}
	return len(text), nil
}

func (c *StreamingTokenCounter) Tokens() (int, error) {
	if c.exact != nil {
		return CountTextTokenContext(context.Background(), c.exact.String(), c.model)
	}
	tokens := c.estimator.Tokens()
	if c.openAI {
		byteFloor := (c.bytes + 7) / 8
		if byteFloor > tokens {
			tokens = byteFloor
		}
	}
	return tokens, nil
}

func (c *StreamingTokenCounter) UsesExactTokenizer() bool {
	return c.exact != nil
}

func (e *TokenEstimator) WriteString(text string) (int, error) {
	for _, r := range text {
		e.consumeRune(r)
	}
	return len(text), nil
}

func (e *TokenEstimator) consumeRune(r rune) {
	if unicode.IsSpace(r) {
		e.currentWordType = 0
		if r == '\n' || r == '\t' {
			e.count += e.m.Newline
		} else {
			e.count += e.m.Space
		}
		return
	}
	if isCJK(r) {
		e.currentWordType = 0
		e.count += e.m.CJK
		return
	}
	if isEmoji(r) {
		e.currentWordType = 0
		e.count += e.m.Emoji
		return
	}
	if isLatinOrNumber(r) {
		newType := 1
		if unicode.IsNumber(r) {
			newType = 2
		}
		if e.currentWordType == 0 || e.currentWordType != newType {
			if newType == 2 {
				e.count += e.m.Number
			} else {
				e.count += e.m.Word
			}
			e.currentWordType = newType
		}
		return
	}
	e.currentWordType = 0
	if isMathSymbol(r) {
		e.count += e.m.MathSymbol
	} else if r == '@' {
		e.count += e.m.AtSign
	} else if isURLDelim(r) {
		e.count += e.m.URLDelim
	} else {
		e.count += e.m.Symbol
	}
}

func (e *TokenEstimator) Tokens() int { return int(math.Ceil(e.count)) + e.m.BasePad }

// EstimateTokenReader counts a complete stream without materializing it. Word
// state lives in the estimator; only an incomplete UTF-8 rune crosses chunks.
func EstimateTokenReader(provider Provider, reader io.Reader) (int, error) {
	e := &TokenEstimator{m: getMultipliers(provider)}
	buf := make([]byte, 64<<10)
	pending := make([]byte, 0, utf8.UTFMax-1)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			part := buf[:n]
			if len(pending) > 0 {
				combined := make([]byte, 0, len(pending)+n)
				combined = append(combined, pending...)
				part = append(combined, part...)
				pending = pending[:0]
			}
			for len(part) > 0 && utf8.FullRune(part) {
				r, size := utf8.DecodeRune(part)
				e.consumeRune(r)
				part = part[size:]
			}
			if len(part) > 0 {
				pending = append(pending, part...)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	for len(pending) > 0 {
		r, size := utf8.DecodeRune(pending)
		e.consumeRune(r)
		pending = pending[size:]
	}
	return e.Tokens(), nil
}

// Provider 定义模型厂商大类
type Provider string

const (
	OpenAI  Provider = "openai"  // 代表 GPT-3.5, GPT-4, GPT-4o
	Gemini  Provider = "gemini"  // 代表 Gemini 1.0, 1.5 Pro/Flash
	Claude  Provider = "claude"  // 代表 Claude 3, 3.5 Sonnet
	Unknown Provider = "unknown" // 兜底默认
)

// multipliers 定义不同厂商的计费权重
type multipliers struct {
	Word       float64 // 英文单词 (每词)
	Number     float64 // 数字 (每连续数字串)
	CJK        float64 // 中日韩字符 (每字)
	Symbol     float64 // 普通标点符号 (每个)
	MathSymbol float64 // 数学符号 (∑,∫,∂,√等，每个)
	URLDelim   float64 // URL分隔符 (/,:,?,&,=,#,%) - tokenizer优化好
	AtSign     float64 // @符号 - 导致单词切分，消耗较高
	Emoji      float64 // Emoji表情 (每个)
	Newline    float64 // 换行符/制表符 (每个)
	Space      float64 // 空格 (每个)
	BasePad    int     // 基础起步消耗 (Start/End tokens)
}

var multipliersMap = map[Provider]multipliers{
	Gemini: {
		Word: 1.15, Number: 2.8, CJK: 0.68, Symbol: 0.38, MathSymbol: 1.05, URLDelim: 1.2, AtSign: 2.5, Emoji: 1.08, Newline: 1.15, Space: 0.2, BasePad: 0,
	},
	Claude: {
		Word: 1.13, Number: 1.63, CJK: 1.21, Symbol: 0.4, MathSymbol: 4.52, URLDelim: 1.26, AtSign: 2.82, Emoji: 2.6, Newline: 0.89, Space: 0.39, BasePad: 0,
	},
	OpenAI: {
		Word: 1.02, Number: 1.55, CJK: 0.85, Symbol: 0.4, MathSymbol: 2.68, URLDelim: 1.0, AtSign: 2.0, Emoji: 2.12, Newline: 0.5, Space: 0.42, BasePad: 0,
	},
}

var mathSymbolSet = func() map[rune]struct{} {
	set := make(map[rune]struct{})
	for _, symbol := range "∑∫∂√∞≤≥≠≈±×÷∈∉∋∌⊂⊃⊆⊇∪∩∧∨¬∀∃∄∅∆∇∝∟∠∡∢°′″‴⁺⁻⁼⁽⁾ⁿ₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎²³¹⁴⁵⁶⁷⁸⁹⁰" {
		set[symbol] = struct{}{}
	}
	return set
}()

var urlDelimiterSet = func() map[rune]struct{} {
	set := make(map[rune]struct{})
	for _, delimiter := range "/:?&=;#%" {
		set[delimiter] = struct{}{}
	}
	return set
}()

// getMultipliers 根据厂商获取权重配置
func getMultipliers(p Provider) multipliers {
	if multiplier, ok := multipliersMap[p]; ok {
		return multiplier
	}
	return multipliersMap[OpenAI]
}

// EstimateToken 计算 Token 数量
func EstimateToken(provider Provider, text string) int {
	e := &TokenEstimator{m: getMultipliers(provider)}
	_, _ = e.WriteString(text)
	return e.Tokens()
}

// 辅助：判断是否为 CJK 字符
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		(r >= 0x3040 && r <= 0x30FF) || // 日文
		(r >= 0xAC00 && r <= 0xD7A3) // 韩文
}

// 辅助：判断是否为单词主体 (字母或数字)
func isLatinOrNumber(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

// 辅助：判断是否为Emoji字符
func isEmoji(r rune) bool {
	// Emoji的Unicode范围
	// 基本范围：0x1F300-0x1F9FF (Emoticons, Symbols, Pictographs)
	// 补充范围：0x2600-0x26FF (Misc Symbols), 0x2700-0x27BF (Dingbats)
	// 表情符号：0x1F600-0x1F64F (Emoticons)
	// 其他：0x1F900-0x1F9FF (Supplemental Symbols and Pictographs)
	return (r >= 0x1F300 && r <= 0x1F9FF) ||
		(r >= 0x2600 && r <= 0x26FF) ||
		(r >= 0x2700 && r <= 0x27BF) ||
		(r >= 0x1F600 && r <= 0x1F64F) ||
		(r >= 0x1F900 && r <= 0x1F9FF) ||
		(r >= 0x1FA00 && r <= 0x1FAFF) // Symbols and Pictographs Extended-A
}

// 辅助：判断是否为数学符号
func isMathSymbol(r rune) bool {
	if _, ok := mathSymbolSet[r]; ok {
		return true
	}
	// Mathematical Operators (U+2200–U+22FF)
	if r >= 0x2200 && r <= 0x22FF {
		return true
	}
	// Supplemental Mathematical Operators (U+2A00–U+2AFF)
	if r >= 0x2A00 && r <= 0x2AFF {
		return true
	}
	// Mathematical Alphanumeric Symbols (U+1D400–U+1D7FF)
	if r >= 0x1D400 && r <= 0x1D7FF {
		return true
	}
	return false
}

// 辅助：判断是否为URL分隔符（tokenizer对这些优化较好）
func isURLDelim(r rune) bool {
	_, ok := urlDelimiterSet[r]
	return ok
}

func EstimateTokenByModel(model, text string) int {
	if text == "" {
		return 0
	}
	estimator := NewTokenEstimator(model)
	_, _ = estimator.WriteString(text)
	return estimator.Tokens()
}

func NewTokenEstimator(model string) *TokenEstimator {
	model = strings.ToLower(model)
	if strings.Contains(model, "gemini") {
		return &TokenEstimator{m: getMultipliers(Gemini)}
	}
	if strings.Contains(model, "claude") {
		return &TokenEstimator{m: getMultipliers(Claude)}
	}
	return &TokenEstimator{m: getMultipliers(OpenAI)}
}
