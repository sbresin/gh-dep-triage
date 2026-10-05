package triage

import (
	"testing"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestDepKey(t *testing.T) {
	const mend = "https://developer.mend.io/api/mc/badges/"
	head := func(h string) func(*model.PR) { return func(p *model.PR) { p.HeadRefName = h } }
	renovate := func(body string) func(*model.PR) {
		return func(p *model.PR) { p.Author, p.HeadRefName, p.Body = "renovate", "renovate/x", body }
	}
	tests := []struct {
		name  string
		title string
		mod   func(*model.PR)
		want  model.DepKey
		ok    bool
	}{
		{"npm", "Bump lodash from 4.17.20 to 4.17.21", head("dependabot/npm_and_yarn/lodash-4.17.21"), model.DepKey{System: "NPM", Name: "lodash", Version: "4.17.21"}, true},
		{"npm scoped in dir", "Bump @types/node from 20.1.0 to 22.0.0 in /frontend", head("dependabot/npm_and_yarn/frontend/types/node-22.0.0"),
			model.DepKey{System: "NPM", Name: "@types/node", Version: "22.0.0"}, true},
		{"pip normalizes", "Bump Django from 4.2 to 5.0", head("dependabot/pip/django-5.0"), model.DepKey{System: "PYPI", Name: "django", Version: "5.0"}, true},
		{"pip separators", "Bump typing_extensions from 4.11.0 to 4.12.0", head("dependabot/pip/typing-extensions-4.12.0"),
			model.DepKey{System: "PYPI", Name: "typing-extensions", Version: "4.12.0"}, true},
		{"pip requirement range", "Update mypy requirement from <2.4,>=2.1 to >=2.1,<2.5", head("dependabot/pip/mypy-gte-2.1-and-lt-2.5"),
			model.DepKey{System: "PYPI", Name: "mypy", Version: "2.5"}, true},
		{"go", "Bump github.com/spf13/cobra from 1.8.0 to 1.9.1", head("dependabot/go_modules/github.com/spf13/cobra-1.9.1"),
			model.DepKey{System: "GO", Name: "github.com/spf13/cobra", Version: "v1.9.1"}, true},
		{"nuget lowercases", "Bump Newtonsoft.Json from 13.0.1 to 13.0.3", head("dependabot/nuget/Newtonsoft.Json-13.0.3"),
			model.DepKey{System: "NUGET", Name: "newtonsoft.json", Version: "13.0.3"}, true},
		{"actions", "Bump actions/checkout from 3 to 4", head("dependabot/github_actions/actions/checkout-4"), model.DepKey{Name: "github.com/actions/checkout"}, true},
		{"docker unsupported", "Bump node from 20 to 22", head("dependabot/docker/node-22"), model.DepKey{}, false},
		{"renovate pypi", "chore(deps): update dependency django to v6.1.1", renovate("![c](" + mend + "confidence/pypi/django/6.0.8/6.1.1?slim=true)"),
			model.DepKey{System: "PYPI", Name: "django", Version: "6.1.1"}, true},
		{"renovate scoped npm", "chore(deps): update dependency @types/node to v22.0.0", renovate("![c](" + mend + "confidence/npm/%40types%2Fnode/20.1.0/22.0.0?slim=true)"),
			model.DepKey{System: "NPM", Name: "@types/node", Version: "22.0.0"}, true},
		{"renovate github-tags", "chore(deps): update actions/setup-python action to v7", renovate("![a](" + mend + "age/github-tags/actions%2Fsetup-python/v7?slim=true)"),
			model.DepKey{Name: "github.com/actions/setup-python"}, true},
		{"renovate badge for another version", "chore(deps): update react monorepo to v19.1.0", renovate("![c](" + mend + "confidence/npm/react-dom/18.3.1/19.0.0?slim=true)"),
			model.DepKey{}, false},
		{"renovate without badge", "chore(deps): update dependency react to v19.0.1", renovate(""), model.DepKey{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DepKey(testPR("acme/api", 1, tt.title, tt.mod))
			if got != tt.want || ok != tt.ok {
				t.Errorf("DepKey = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
