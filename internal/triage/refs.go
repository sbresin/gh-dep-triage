package triage

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var prRefRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#([0-9]+)$`)

type RefKind int

const (
	RefPR RefKind = iota
	RefGroup
)

type Ref struct {
	Raw     string
	Kind    RefKind
	PR      model.PRRef
	Package string // slugged, lower-case
	Target  string
	Bump    string // empty when the ref has no ~suffix
}

type RefError struct {
	Code       string
	Ref        string
	Message    string
	Candidates []string
}

func (e *RefError) Error() string { return e.Message }

func invalidRef(s, why string) error {
	return &RefError{Code: "invalid_ref", Ref: s, Message: fmt.Sprintf("invalid ref %q: %s", s, why)}
}

func ParseRef(s string) (Ref, error) {
	if m := prRefRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 {
			return Ref{}, invalidRef(s, "PR number must be positive")
		}
		return Ref{Raw: s, Kind: RefPR, PR: model.PRRef{Repo: m[1], Number: n}}, nil
	}
	rest, ok := strings.CutPrefix(s, "group:")
	if !ok {
		return Ref{}, invalidRef(s, "expected owner/repo#123 or group:<package>@<target>[~<bump>]")
	}
	at := strings.LastIndex(rest, "@")
	if at <= 0 || at == len(rest)-1 {
		return Ref{}, invalidRef(s, "expected group:<package>@<target>")
	}
	pkg, version := rest[:at], rest[at+1:]
	bump := ""
	// A ~ with nothing before it starts a range such as "~> 7.0", not a
	// suffix, unless all that follows is a bump ("@~major" lacks a version).
	if _, bare := bumpRank[strings.TrimPrefix(version, "~")]; bare && strings.HasPrefix(version, "~") {
		return Ref{}, invalidRef(s, "suffix must be ~major, ~minor, ~patch or ~unknown after a version")
	}
	if tilde := strings.LastIndex(version, "~"); tilde > 0 {
		v, b := version[:tilde], version[tilde+1:]
		if _, known := bumpRank[b]; !known {
			return Ref{}, invalidRef(s, "suffix must be ~major, ~minor, ~patch or ~unknown after a version")
		}
		version, bump = v, b
	}
	return Ref{Raw: s, Kind: RefGroup, Package: slug(pkg), Target: version, Bump: bump}, nil
}

func ResolveGroup(groups []*model.Group, r Ref) (*model.Group, error) {
	base := "group:" + r.Package + "@" + r.Target
	var matches []*model.Group
	for _, g := range groups {
		if g.TargetVersion == "" || BaseGroupID(g.PackageKey, g.TargetVersion) != base {
			continue
		}
		if r.Bump != "" && g.Bump != r.Bump {
			continue
		}
		matches = append(matches, g)
	}
	switch len(matches) {
	case 0:
		return nil, &RefError{Code: "not_found", Ref: r.Raw, Message: fmt.Sprintf("no group matches %s", r.Raw)}
	case 1:
		return matches[0], nil
	default:
		cands := make([]string, len(matches))
		for i, g := range matches {
			cands[i] = g.ID
		}
		return nil, &RefError{Code: "ambiguous_ref", Ref: r.Raw, Candidates: cands,
			Message: fmt.Sprintf("%s matches %d groups; add a ~<bump> suffix", r.Raw, len(matches))}
	}
}
