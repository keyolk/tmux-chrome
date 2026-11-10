package app

import (
	"strings"

	"github.com/keyolk/tuikit/fuzzy"
)

// scope is what the search box searches.
type scope int

const (
	scopeAll scope = iota
	scopeTabs
	scopeGroups
	scopeCount
)

func (s scope) String() string {
	switch s {
	case scopeTabs:
		return "탭"
	case scopeGroups:
		return "그룹"
	}
	return "그룹+탭"
}

// query is the parsed search box. Plain words are fuzzy and must all match;
// @word fuzzy-matches the group title, /word is a substring of the URL, and
// !word drops anything whose title, URL or group contains it.
type query struct {
	terms    []string
	groups   []string
	urls     []string
	excludes []string
}

func parseQuery(s string) query {
	var q query
	for _, f := range strings.Fields(s) {
		switch {
		case len(f) > 1 && f[0] == '@':
			q.groups = append(q.groups, f[1:])
		case len(f) > 1 && f[0] == '/':
			q.urls = append(q.urls, strings.ToLower(f[1:]))
		case len(f) > 1 && f[0] == '!':
			q.excludes = append(q.excludes, strings.ToLower(f[1:]))
		case f == "@" || f == "/" || f == "!":
			// A bare operator is a word being typed; it filters nothing yet.
		default:
			q.terms = append(q.terms, f)
		}
	}
	return q
}

func (q query) empty() bool {
	return len(q.terms)+len(q.groups)+len(q.urls)+len(q.excludes) == 0
}

// fuzzyAll scores text against every term; all must match.
func fuzzyAll(text string, terms []string) (int, bool) {
	total := 0
	for _, t := range terms {
		s, _, ok := fuzzy.Score(text, t)
		if !ok {
			return 0, false
		}
		total += s
	}
	return total, true
}

// highlight is where the plain terms hit text, for drawing. It is cosmetic:
// a term that does not hit this text on its own just adds nothing.
func highlight(text string, terms []string) []int {
	var pos []int
	for _, t := range terms {
		if _, p, ok := fuzzy.Score(text, t); ok {
			pos = append(pos, p...)
		}
	}
	return pos
}

func excluded(q query, fields ...string) bool {
	for _, x := range q.excludes {
		for _, f := range fields {
			if strings.Contains(strings.ToLower(f), x) {
				return true
			}
		}
	}
	return false
}

func groupFilterOK(q query, groupTitle string) bool {
	_, ok := fuzzyAll(groupTitle, q.groups)
	return ok
}

// matchTab scores a tab. In scopeAll the group title counts as text too, so
// "ops grafana" finds the grafana tab inside the ops group.
func matchTab(q query, sc scope, title, url, groupTitle string) (int, bool) {
	if excluded(q, title, url, groupTitle) || !groupFilterOK(q, groupTitle) {
		return 0, false
	}
	lu := strings.ToLower(url)
	for _, u := range q.urls {
		if !strings.Contains(lu, u) {
			return 0, false
		}
	}
	text := title + " " + url
	if sc == scopeAll {
		text += " " + groupTitle
	}
	return fuzzyAll(text, q.terms)
}

// matchGroup scores a group on its title alone.
func matchGroup(q query, title string) (int, bool) {
	if excluded(q, title) || !groupFilterOK(q, title) || len(q.urls) > 0 {
		return 0, false
	}
	return fuzzyAll(title, q.terms)
}
