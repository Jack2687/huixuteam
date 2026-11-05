package store

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type CustomMenu struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	URL            string `json:"url"`
	IconSVG        string `json:"icon_svg"`
	Visibility     string `json:"visibility"`
	Enabled        bool   `json:"enabled"`
	OpenInNewTab   bool   `json:"open_in_new_tab"`
	HideOpenButton bool   `json:"hide_open_button"`
	SortOrder      int    `json:"sort_order"`
}

func (v CustomMenu) VisibleTo(u User) bool {
	return !u.Disabled && v.Enabled && (v.Visibility == "user" || (v.Visibility == "admin" && u.Role == "admin"))
}

func (s *Store) CustomMenu(ctx context.Context, u User, id string) (CustomMenu, error) {
	v, err := s.SiteSettings(ctx)
	if err != nil {
		return CustomMenu{}, err
	}
	for _, item := range v.CustomMenus {
		if item.ID != id {
			continue
		}
		if !item.Enabled {
			return CustomMenu{}, ErrNotFound
		}
		if !item.VisibleTo(u) {
			return CustomMenu{}, ErrForbidden
		}
		return item, nil
	}
	return CustomMenu{}, ErrNotFound
}

var customMenuID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func (s *Store) SaveCustomMenus(ctx context.Context, actor string, items []CustomMenu) error {
	if len(items) > 20 {
		return invalid("自定义菜单最多20项")
	}
	if items == nil {
		items = []CustomMenu{}
	}
	seen := map[string]bool{}
	for i := range items {
		v := &items[i]
		if v.ID == "" {
			v.ID = ID()
		}
		v.Label = strings.TrimSpace(v.Label)
		v.URL = strings.TrimSpace(v.URL)
		if !customMenuID.MatchString(v.ID) || seen[v.ID] {
			return invalid("菜单标识无效或重复")
		}
		seen[v.ID] = true
		if v.Label == "" || utf8.RuneCountInString(v.Label) > 60 || strings.IndexFunc(v.Label, unicode.IsControl) >= 0 {
			return invalid("菜单名称须为1～60个字符")
		}
		if v.Visibility != "user" && v.Visibility != "admin" {
			return invalid("菜单可见角色无效")
		}
		u, err := url.Parse(v.URL)
		if err != nil || len(v.URL) > 2048 || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || strings.Contains(v.URL, "\\") || strings.IndexFunc(v.URL, unicode.IsControl) >= 0 {
			return invalid("页面URL须为完整的HTTP或HTTPS地址，不能包含账号密码")
		}
		v.IconSVG, err = validateMenuSVG(v.IconSVG)
		if err != nil {
			return err
		}
		v.SortOrder = i
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE site_settings SET custom_menus=$1,updated_at=now() WHERE id", raw); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "site.custom_menus", "site", map[string]any{"count": len(items)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Only static SVG drawing primitives are accepted. Render them as images, never
// inline HTML; disallow script, event handlers, embedded HTML and external refs.
func validateMenuSVG(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	bad := func() (string, error) {
		return "", invalid("SVG图标须小于16KB，且只能包含静态图形，不能包含脚本、样式或外部资源")
	}
	if len(raw) > 16*1024 {
		return bad()
	}
	tags := strings.Fields("svg g path rect circle ellipse line polyline polygon defs linearGradient radialGradient stop clipPath mask title desc")
	attrs := strings.Fields("id viewBox width height x y x1 y1 x2 y2 cx cy r rx ry d points fill fill-rule fill-opacity stroke stroke-width stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset stroke-opacity opacity transform preserveAspectRatio offset stop-color stop-opacity gradientUnits gradientTransform spreadMethod fx fy fr clip-path clip-rule clipPathUnits mask maskUnits maskContentUnits")
	allowed := func(list []string, value string) bool {
		for _, v := range list {
			if v == value {
				return true
			}
		}
		return false
	}
	localRef := regexp.MustCompile(`^url\(#[a-zA-Z0-9_-]+\)$`)
	d := xml.NewDecoder(strings.NewReader(raw))
	var result bytes.Buffer
	e := xml.NewEncoder(&result)
	depth, count := 0, 0
	rootSeen := false
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return bad()
		}
		count++
		if count > 2000 {
			return bad()
		}
		switch t := token.(type) {
		case xml.StartElement:
			if !allowed(tags, t.Name.Local) || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") {
				return bad()
			}
			if depth == 0 {
				if rootSeen || t.Name.Local != "svg" {
					return bad()
				}
				rootSeen = true
			}
			depth++
			if depth > 32 {
				return bad()
			}
			out := xml.StartElement{Name: xml.Name{Local: t.Name.Local}}
			if depth == 1 {
				out.Attr = append(out.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
			}
			for _, a := range t.Attr {
				if a.Name.Local == "xmlns" && a.Name.Space == "" {
					if a.Value != "http://www.w3.org/2000/svg" {
						return bad()
					}
					continue
				}
				if a.Name.Space != "" || !allowed(attrs, a.Name.Local) {
					return bad()
				}
				lower := strings.ToLower(a.Value)
				if strings.ContainsAny(a.Value, "\\<>") || strings.Contains(lower, "javascript:") || strings.Contains(lower, "data:") || strings.Contains(lower, "@import") || (strings.Contains(lower, "url") && !localRef.MatchString(a.Value)) {
					return bad()
				}
				out.Attr = append(out.Attr, a)
			}
			if e.EncodeToken(out) != nil {
				return bad()
			}
		case xml.EndElement:
			depth--
			if e.EncodeToken(xml.EndElement{Name: xml.Name{Local: t.Name.Local}}) != nil {
				return bad()
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return bad()
			}
			if depth > 0 {
				if e.EncodeToken(t) != nil {
					return bad()
				}
			}
		case xml.Comment:
		case xml.ProcInst:
			if t.Target != "xml" || rootSeen {
				return bad()
			}
		default:
			return bad()
		}
	}
	if !rootSeen || depth != 0 || e.Flush() != nil {
		return bad()
	}
	return result.String(), nil
}
