package controller

import (
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type tokenGroupProfileRequest struct {
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	Description  string   `json:"description"`
	Enabled      *bool    `json:"enabled"`
	DisplayOrder int      `json:"display_order"`
	Recommended  bool     `json:"recommended"`
	RouteGroups  []string `json:"route_groups"`
	ModelScope   []string `json:"model_scope"`
}

func profileResponse(p *model.TokenGroupProfile) gin.H {
	return gin.H{"id": p.ID, "name": p.Name, "slug": p.Slug, "description": p.Description, "enabled": p.Enabled, "display_order": p.DisplayOrder, "recommended": p.Recommended, "route_groups": p.GetRouteGroups(), "model_scope": p.GetModelScope()}
}

func ListTokenGroupProfiles(c *gin.Context) {
	var rows []model.TokenGroupProfile
	q := model.DB.Order("display_order asc").Order("id asc")
	if c.GetBool("is_admin") { /* admins see disabled profiles too */
	} else {
		q = q.Where("enabled = ?", true)
	}
	if err := q.Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, profileResponse(&rows[i]))
	}
	common.ApiSuccess(c, out)
}

func AdminListTokenGroupProfiles(c *gin.Context) {
	c.Set("is_admin", true)
	ListTokenGroupProfiles(c)
}

func CreateTokenGroupProfile(c *gin.Context) {
	var req tokenGroupProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Name == "" || len(req.Name) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "分组名称不能为空且不超过100个字符"})
		return
	}
	if req.Slug == "" {
		req.Slug = strings.ToLower(strings.ReplaceAll(req.Name, " ", "-"))
	}
	p := &model.TokenGroupProfile{Name: req.Name, Slug: req.Slug, Description: req.Description, DisplayOrder: req.DisplayOrder, Recommended: req.Recommended, Enabled: true}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if err := p.SetRouteGroups(req.RouteGroups); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := p.SetModelScope(req.ModelScope); err != nil {
		common.ApiError(c, err)
		return
	}
	if len(req.RouteGroups) == 0 {
		p.RouteGroups = "[]"
	}
	if err := model.DB.Create(p).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, profileResponse(p))
}

func UpdateTokenGroupProfile(c *gin.Context) {
	var p model.TokenGroupProfile
	if err := model.DB.First(&p, c.Param("id")).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var req tokenGroupProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		p.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Slug) != "" {
		p.Slug = strings.TrimSpace(req.Slug)
	}
	p.Description = req.Description
	p.DisplayOrder = req.DisplayOrder
	p.Recommended = req.Recommended
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if err := p.SetRouteGroups(req.RouteGroups); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := p.SetModelScope(req.ModelScope); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Save(&p).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateTokenGroupProfileCache(p.ID)
	common.ApiSuccess(c, profileResponse(&p))
}

func DeleteTokenGroupProfile(c *gin.Context) {
	var p model.TokenGroupProfile
	if err := model.DB.First(&p, c.Param("id")).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Delete(&p).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateTokenGroupProfileCache(p.ID)
	common.ApiSuccess(c, nil)
}

// TokenGroupProfileHelp returns profiles matching a model name for the token creation helper.
func TokenGroupProfileHelp(c *gin.Context) {
	modelName := strings.TrimSpace(c.Query("model"))
	normalize := func(value string) string {
		value = strings.ToLower(value)
		value = strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(value)
		return value
	}
	normalizedModel := normalize(modelName)
	userGroup, _ := model.GetUserGroup(c.GetInt("id"), false)
	usableGroups := service.GetUserUsableGroups(userGroup)
	groupRatios := ratio_setting.GetGroupRatioCopy()
	groupsOut := make([]gin.H, 0, len(usableGroups))
	for groupName, description := range usableGroups {
		if groupName == "auto" {
			continue
		}
		if _, ok := groupRatios[groupName]; !ok {
			continue
		}
		matched := modelName == ""
		if modelName != "" {
			for _, enabledModel := range model.GetGroupEnabledModels(groupName) {
				n := normalize(enabledModel)
				if n == normalizedModel || strings.Contains(n, normalizedModel) || strings.Contains(normalizedModel, n) {
					matched = true
					break
				}
			}
		}
		groupsOut = append(groupsOut, gin.H{"name": groupName, "desc": description, "ratio": service.GetUserGroupRatio(userGroup, groupName), "matched": matched})
	}
	sort.Slice(groupsOut, func(i, j int) bool { return groupsOut[i]["name"].(string) < groupsOut[j]["name"].(string) })
	var rows []model.TokenGroupProfile
	q := model.DB.Where("enabled = ?", true).Order("recommended desc").Order("display_order asc")
	if err := q.Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	matchedCount := 0
	for i := range rows {
		scope := rows[i].GetModelScope()
		if modelName != "" {
			matched := false
			if len(scope) > 0 {
				for _, s := range scope {
					if s == "*" || normalize(s) == normalizedModel || strings.Contains(normalize(s), normalizedModel) || strings.Contains(normalizedModel, normalize(s)) {
						matched = true
						break
					}
				}
			} else {
				// An empty model scope means "all models in the configured
				// route groups". This keeps older/admin-created profiles useful
				// without requiring a second model-maintenance workflow.
				for _, group := range rows[i].GetRouteGroups() {
					for _, enabledModel := range model.GetGroupEnabledModels(group) {
						normalizedEnabledModel := normalize(enabledModel)
						if normalizedEnabledModel == normalizedModel || strings.Contains(normalizedEnabledModel, normalizedModel) || strings.Contains(normalizedModel, normalizedEnabledModel) {
							matched = true
							break
						}
					}
					if matched {
						break
					}
				}
			}
			if !matched {
				continue
			}
			matchedCount++
		}
		out = append(out, profileResponse(&rows[i]))
	}
	// Keep the helper useful when administrators configured route groups but
	// model metadata has not been synced yet. The UI labels these as general
	// candidates instead of claiming an exact model match.
	if modelName != "" && matchedCount == 0 {
		out = out[:0]
		for i := range rows {
			out = append(out, profileResponse(&rows[i]))
		}
	}
	common.ApiSuccess(c, gin.H{"model": modelName, "profiles": out, "groups": groupsOut, "exact_match": matchedCount > 0, "available_groups": ratio_setting.GetGroupRatioCopy()})
}
