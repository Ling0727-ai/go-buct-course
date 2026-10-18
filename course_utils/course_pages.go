package course_utils

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
)

const (
	maxCoursePages = 100
	maxCourseDepth = 8
	maxCourseBody  = 8 << 20
)

// Only literal navigation is recognized; scripts are never executed.
var courseLocation = regexp.MustCompile(`(?:window\.)?location(?:\.href)?\s*=\s*['"]([^'"]+)['"]|(?:window\.)?location\.replace\s*\(\s*['"]([^'"]+)['"]`)
var courseQuotedURL = regexp.MustCompile(`['"]([^'"]+)['"]`)
var courseRefresh = regexp.MustCompile(`(?i)url\s*=\s*(.+)`)

func resolveCourseURL(base, raw string) (*url.URL, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	rel, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	resolved := baseURL.ResolveReference(rel)
	resolved.Path = path.Clean(resolved.Path)
	resolved.RawPath = ""
	resolved.Fragment = ""
	resolved.RawQuery = resolved.Query().Encode()
	return resolved, nil
}

func (m *Manager) allowedHomeworkURL(candidate *url.URL) bool {
	base, err := url.Parse(m.BaseURL)
	if err != nil || candidate.Scheme != base.Scheme || !strings.EqualFold(candidate.Host, base.Host) || candidate.User != nil {
		return false
	}
	switch candidate.Path {
	case "/meol/jpk/course/layout/newpage/index.jsp", "/meol/jpk/course/course_column_preview_transfer.jsp", "/meol/buildless/colUrlStuView.do", "/meol/common/hw/student/hwtask.jsp", "/meol/common/hw/student/hwtask_blended.jsp":
		return true
	default:
		return false
	}
}

type coursePage struct {
	url     *url.URL
	referer string
	depth   int
}

// walkHomeworkPages follows bounded same-origin navigation, frames and pagination.
func (m *Manager) walkHomeworkPages(startURL string, visit func(string, *goquery.Document) error) error {
	start, err := resolveCourseURL(m.BaseURL+"/", startURL)
	if err != nil || !m.allowedHomeworkURL(start) {
		return fmt.Errorf("invalid or unsupported course list URL")
	}
	queue := []coursePage{{url: start, referer: m.BaseURL + "/"}}
	scheduled := map[string]bool{start.String(): true}
	signatures := make(map[[32]byte]bool)
	for fetched := 0; len(queue) > 0; {
		item := queue[0]
		queue = queue[1:]
		if item.depth > maxCourseDepth || fetched >= maxCoursePages {
			return fmt.Errorf("course page traversal limit exceeded; results may be incomplete")
		}
		fetched++
		doc, status, location, err := m.fetchCoursePage(item.url, item.referer)
		if err != nil {
			return err
		}
		add := func(raw string, depth int, front bool) {
			target, e := resolveCourseURL(item.url.String(), raw)
			if e != nil || !m.allowedHomeworkURL(target) || scheduled[target.String()] {
				return
			}
			scheduled[target.String()] = true
			next := coursePage{url: target, referer: item.url.String(), depth: depth}
			if front {
				queue = append([]coursePage{next}, queue...)
			} else {
				queue = append(queue, next)
			}
		}
		if status >= 300 && status < 400 {
			target, e := resolveCourseURL(item.url.String(), location)
			if e != nil {
				return e
			}
			if strings.Contains(strings.ToLower(target.Path), "login") {
				return fmt.Errorf("course session expired")
			}
			if location == "" {
				return fmt.Errorf("course redirect has no Location")
			}
			add(location, item.depth+1, true)
			continue
		}
		if doc.Find("input[type='password']").Length() > 0 {
			return fmt.Errorf("course session expired")
		}
		if err := visit(item.url.String(), doc); err != nil {
			return err
		}

		// A repeated full list means the server ignored the requested page number.
		query := item.url.Query()
		table := doc.Find("table.valuelist").First()
		repeated := false
		if table.Length() > 0 && query.Get("pagingPage") != "" {
			markup, _ := table.Html()
			// Scope repeated content to one pagination series. Different columns
			// can share the same first page but have distinct later pages.
			series := *item.url
			seriesQuery := series.Query()
			seriesQuery.Del("pagingPage")
			series.RawQuery = seriesQuery.Encode()
			signature := sha256.Sum256([]byte(series.String() + "\x00" + markup))
			repeated = signatures[signature]
			signatures[signature] = true
		}
		doc.Find("iframe[src], frame[src]").Each(func(_ int, s *goquery.Selection) { raw, _ := s.Attr("src"); add(raw, item.depth+1, false) })
		doc.Find("meta[http-equiv]").Each(func(_ int, s *goquery.Selection) {
			h, _ := s.Attr("http-equiv")
			content, _ := s.Attr("content")
			if strings.EqualFold(h, "refresh") {
				if match := courseRefresh.FindStringSubmatch(content); len(match) > 1 {
					add(strings.Trim(match[1], ` "'`), item.depth+1, false)
				}
			}
		})
		literal := func(text string) {
			for _, match := range courseLocation.FindAllStringSubmatch(text, -1) {
				for _, raw := range match[1:] {
					if raw != "" {
						add(raw, item.depth+1, false)
					}
				}
			}
		}
		doc.Find("script:not([src])").Each(func(_ int, s *goquery.Selection) { literal(s.Text()) })
		doc.Find("a[href], a[onclick]").Each(func(_ int, s *goquery.Selection) {
			href, _ := s.Attr("href")
			if target, e := resolveCourseURL(item.url.String(), href); e == nil && target.Path == item.url.Path {
				if !repeated {
					add(href, item.depth, false)
				}
			} else {
				add(href, item.depth+1, false)
			}
			onclick, _ := s.Attr("onclick")
			literal(onclick)
			for _, match := range courseQuotedURL.FindAllStringSubmatch(onclick, -1) {
				if strings.Contains(match[1], "columnId=") || strings.Contains(match[1], "pagingPage=") {
					if !repeated {
						add(match[1], item.depth, false)
					}
				}
			}
		})
		current, _ := strconv.Atoi(query.Get("pagingPage"))
		per, _ := strconv.Atoi(query.Get("pagingNumberPer"))
		if !repeated && current > 0 && per > 0 && table.Find("tr").FilterFunction(func(_ int, row *goquery.Selection) bool { return row.ChildrenFiltered("td").Length() > 0 }).Length() >= per {
			query.Set("pagingPage", strconv.Itoa(current+1))
			next := *item.url
			next.RawQuery = query.Encode()
			add(next.String(), item.depth, false)
		}
	}
	return nil
}

// Disable automatic redirects without mutating the shared authenticated client.
func (m *Manager) fetchCoursePage(pageURL *url.URL, referer string) (*goquery.Document, int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	client := *m.Client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, resp.StatusCode, resp.Header.Get("Location"), nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, "", fmt.Errorf("%w: status code %d", exceptions.ErrNetwork, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCourseBody+1))
	if err != nil {
		return nil, 0, "", err
	}
	if len(body) > maxCourseBody {
		return nil, 0, "", fmt.Errorf("course page exceeds size limit")
	}
	var reader io.Reader = bytes.NewReader(body)
	// Legacy pages default to GBK; UTF-8 pages must not be decoded as GBK.
	if !utf8.Valid(body) {
		reader, err = charset.NewReaderLabel("gbk", reader)
		if err != nil {
			return nil, 0, "", err
		}
	}
	doc, err := goquery.NewDocumentFromReader(reader)
	return doc, resp.StatusCode, "", err
}
