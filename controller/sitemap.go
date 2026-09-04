package controller

import (
	"encoding/xml"
	"net/http"
	"net/url"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type sitemapURLSet struct {
	XMLName xml.Name       `xml:"urlset"`
	Xmlns   string         `xml:"xmlns,attr"`
	URLs    []sitemapEntry `xml:"url"`
}

type sitemapEntry struct {
	Loc string `xml:"loc"`
}

// Sitemap returns the public URL inventory, including currently visible model pages.
func Sitemap(c *gin.Context) {
	const siteURL = "https://viralapi.ai"
	entries := []sitemapEntry{
		{Loc: siteURL + "/"},
		{Loc: siteURL + "/about/"},
		{Loc: siteURL + "/privacy-policy"},
		{Loc: siteURL + "/user-agreement"},
	}

	if middleware.IsHeaderNavModulePublic("pricing") {
		entries = append(entries, sitemapEntry{Loc: siteURL + "/pricing/"})
		usableGroups := service.GetUserUsableGroups("")
		seen := make(map[string]struct{})
		for _, item := range model.GetPricing() {
			if item.ModelName == "" || !pricingVisibleToGroups(item.EnableGroup, usableGroups) {
				continue
			}
			if _, ok := seen[item.ModelName]; ok {
				continue
			}
			seen[item.ModelName] = struct{}{}
			entries = append(entries, sitemapEntry{Loc: siteURL + "/pricing/" + url.PathEscape(item.ModelName) + "/"})
		}
	}
	if middleware.IsHeaderNavModulePublic("rankings") {
		entries = append(entries, sitemapEntry{Loc: siteURL + "/rankings/"})
	}

	body, err := xml.Marshal(sitemapURLSet{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: entries})
	if err != nil {
		common.SysError("failed to generate sitemap: " + err.Error())
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Cache-Control", "public, max-age=300, s-maxage=900")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", append([]byte(xml.Header), body...))
}

func pricingVisibleToGroups(groups []string, usable map[string]string) bool {
	if common.StringsContains(groups, "all") {
		return true
	}
	for _, group := range groups {
		if _, ok := usable[group]; ok {
			return true
		}
	}
	return false
}
