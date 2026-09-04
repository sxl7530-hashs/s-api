package model

import (
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var tokenGroupProfileCache sync.Map

// TokenGroupProfile is an administrator-defined business preset for API keys.
// RouteGroups and ModelScope are JSON arrays kept as TEXT for cross-database support.
type TokenGroupProfile struct {
	ID           int            `json:"id"`
	Name         string         `json:"name" gorm:"size:100;uniqueIndex"`
	Slug         string         `json:"slug" gorm:"size:100;uniqueIndex"`
	Description  string         `json:"description" gorm:"type:text"`
	Enabled      bool           `json:"enabled"`
	DisplayOrder int            `json:"display_order" gorm:"index"`
	Recommended  bool           `json:"recommended"`
	RouteGroups  string         `json:"-" gorm:"type:text"`
	ModelScope   string         `json:"-" gorm:"type:text"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

func (p *TokenGroupProfile) GetRouteGroups() []string {
	var v []string
	if p == nil || strings.TrimSpace(p.RouteGroups) == "" {
		return v
	}
	if common.UnmarshalJsonStr(p.RouteGroups, &v) != nil {
		return []string{}
	}
	return v
}
func (p *TokenGroupProfile) GetModelScope() []string {
	var v []string
	if p == nil || strings.TrimSpace(p.ModelScope) == "" {
		return v
	}
	if common.UnmarshalJsonStr(p.ModelScope, &v) != nil {
		return []string{}
	}
	return v
}
func (p *TokenGroupProfile) SetRouteGroups(v []string) error {
	b, e := common.Marshal(v)
	if e == nil {
		p.RouteGroups = string(b)
	}
	return e
}
func (p *TokenGroupProfile) SetModelScope(v []string) error {
	b, e := common.Marshal(v)
	if e == nil {
		p.ModelScope = string(b)
	}
	return e
}

func GetEnabledTokenGroupProfile(id int) (*TokenGroupProfile, error) {
	if value, ok := tokenGroupProfileCache.Load(id); ok {
		profile := value.(TokenGroupProfile)
		return &profile, nil
	}
	var profile TokenGroupProfile
	if err := DB.Where("id = ? AND enabled = ?", id, true).First(&profile).Error; err != nil {
		return nil, err
	}
	tokenGroupProfileCache.Store(id, profile)
	return &profile, nil
}

func InvalidateTokenGroupProfileCache(id int) { tokenGroupProfileCache.Delete(id) }
