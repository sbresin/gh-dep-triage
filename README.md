<div align="center">

# 🧹 gh dep-triage

**Triage, merge and unblock Dependabot & Renovate PRs across all your repos, from one terminal.**

[![ci](https://github.com/sbresin/gh-dep-triage/actions/workflows/ci.yml/badge.svg)](https://github.com/sbresin/gh-dep-triage/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/sbresin/gh-dep-triage?include_prereleases&sort=semver)](https://github.com/sbresin/gh-dep-triage/releases)
[![go](https://img.shields.io/github/go-mod/go-version/sbresin/gh-dep-triage)](go.mod)
[![gh extension](https://img.shields.io/badge/gh-extension-2ea44f?logo=github)](https://cli.github.com/manual/gh_extension)

</div>

```
dep-triage | sort package | 0 marked | queue ✓ 1 ⟳ 1 ⏳ 1 ✗ 1 | user octocat
┌─ PRs ──────────────────────────────────────────────────────────────────────┐┌─ Queue 4 · ✓ 1 ⟳ 1 ⏳ 1 ✗ 1 ───────────┐
│  󰄱 acme/ops#9  zod -> 3.0.1 [patch]                              review  --││✓ api#1  lodash -> 4.17.21 [patch]  a...│
│                                                                            ││✗ web#2  lodash -> 4.17.21 [patch]  h...│
│                                                                            ││⟳ api#3  axios -> 1.7.0 [minor]  appr...│
│                                                                            ││⏳ api#4  eslint -> 9.1.0 [major]  qu...│
│                                                                            ││                                        │
│                                                                            ││                                        │
│                                                                            ││                                        │
└────────────────────────────────────────────────────────────────────────────┘└────────────────────────────────────────┘
tab queue  pgup/dn  spc mark  c confirm  enter fold  s sort  o open  d details  g reload  q quit
```

When you get added as a reviewer on dozens of bot PRs every week, you end up
with the same `lodash` bump spread across twelve repos. `gh dep-triage`
finds every Dependabot/Renovate PR that is waiting for your review (or that
you already approved). It groups them by package and target version, works
out what is blocking each one, and approves and merges them in bulk, all
under guardrails.

- ⚡ **Fast.** Batched, parallel GraphQL loads around 200 PRs in seconds.
- 📦 **Grouped.** One row per `package@version` across all repos, labelled patch, minor or major.
- 🩺 **Diagnosed.** Each PR is marked `ready`, `merging` or `blocked` with a reason (failing checks, behind base, conflicts, superseded, stale, …).
- 🔎 **Risk-aware.** Stars, the OpenSSF Scorecard and supply-chain findings (malicious, brand-new release, typosquat, deprecated) for each dependency, from [deps.dev](https://deps.dev).
- 🧰 **Unblocks.** Asks the bot to rebase or recreate, re-runs flaky jobs, requests reviews, closes superseded PRs.
- 🛡️ **Guarded.** It never merges with a failing check, never uses an admin bypass, pins every action to the commit you looked at, and treats major bumps as opt-in.
- 🤖 **Agent-ready.** Every command has `--json` output, a stable schema and dry runs by default, and an agent skill ships inside the binary.
- 🔑 **Zero setup.** It uses your existing `gh` login. No tokens, no runtimes.

## Install

```sh
gh extension install sbresin/gh-dep-triage
gh extension upgrade dep-triage   # later
```

The only requirement is an authenticated [`gh`](https://cli.github.com/) (`gh auth login`).

## Interactive mode

```sh
gh dep-triage                      # PRs requesting your review + PRs you approved
gh dep-triage --team acme/platform # PRs requesting review from a team
```

Mark PRs with <kbd>space</kbd> and press <kbd>c</kbd> to queue Approve+Merge.
The queue runs in the background while you keep triaging.

| key | action |
|---|---|
| <kbd>j</kbd>/<kbd>k</kbd>, <kbd>pgup</kbd>/<kbd>pgdn</kbd> | move |
| <kbd>space</kbd> / <kbd>c</kbd> | mark for Approve+Merge / confirm |
| <kbd>enter</kbd> | fold or unfold a group |
| <kbd>s</kbd> | cycle sort: package, severity, checks, repo |
| <kbd>d</kbd> | PR details popup: blockers, deps.dev risk, description |
| <kbd>r</kbd> / <kbd>R</kbd> / <kbd>x</kbd> | rebase / re-run failed checks / close superseded |
| <kbd>o</kbd> | open in browser |
| <kbd>g</kbd> | reload |
| <kbd>tab</kbd> | switch to the queue (<kbd>x</kbd> cancel, <kbd>r</kbd> retry, <kbd>p</kbd> pause, <kbd>C</kbd> clear) |
| <kbd>q</kbd> | quit |

## Command line & agent mode

Every mutating command is a **dry run unless you pass `--yes`**.

```sh
gh dep-triage list --json                        # snapshot: groups, PRs, blockers, suggested commands
gh dep-triage list --status ready --bump patch,minor  # just the easy ones, one line per PR
gh dep-triage show acme/api#12 --logs            # body, release notes, files, failed-job logs

gh dep-triage merge group:lodash@4.17.21         # dry run: what would happen?
gh dep-triage merge group:lodash@4.17.21 --yes   # do it

gh dep-triage rebase   acme/api#12 --yes         # ask the bot to rebase
gh dep-triage recreate acme/api#12 --yes         # ask the bot to recreate
gh dep-triage rerun    acme/web#3  --yes         # re-run failed Actions jobs
gh dep-triage request-review acme/api#12 --reviewer acme/platform --yes
gh dep-triage close    acme/api#9 --reason superseded --yes

echo '[{"action":"merge","ref":"acme/api#12","headOid":"abc123"}]' \
  | gh dep-triage apply --plan - --yes           # run a plan pinned to the inspected commit
```

**Refs** are either `owner/repo#123` or `group:<package>@<version>[~major|minor|patch]`.

**Exit codes:** `0` ok · `1` error · `2` partial (something failed) · `3` everything was denied by policy.

### Use it from a coding agent

The binary ships an agent skill that teaches the workflow and the safety rules:

```sh
gh dep-triage skill            # print it, e.g. "run `gh dep-triage skill` and follow it"
gh dep-triage skill install    # or write it to ~/.agents/skills/gh-dep-triage/SKILL.md
```

## Guardrails

These **hard rules** always apply, in the TUI and on the CLI:

- Never merge or enable auto-merge while any check is failing.
- Never use admin or bypass merges.
- Never approve or merge a version that deps.dev flags as malicious.
- Every mutation passes the head commit it was evaluated against. If the branch has moved, the item fails with `head_changed`.
- Only PRs authored by the configured bots are touched.

These **soft rules** apply on the CLI only, because in the TUI you are the policy:

- Major (and unknown) bumps need `--allow-major`.
- Repo allow and deny lists.
- `close` only for `superseded` or `stale` PRs that actually have that blocker.

## Configuration

This file is optional. It lives at `~/.config/gh-dep-triage/config.yml` (or `$XDG_CONFIG_HOME`):

```yaml
policy:
  allowMajor: false
  repos:
    allow: ["acme/*"]   # empty = all repos
    deny: ["acme/legacy-*"]
bots: [dependabot, renovate]
defaults:
  team: acme/platform
  limit: 200
```

`list`, `show` and the TUI send each dependency's name and version to [deps.dev](https://deps.dev) to look up risk data. If deps.dev is unreachable, the tool shows a warning and leaves the risk data out.

## Development

```sh
go test -race ./...
golangci-lint run
go build -o gh-dep-triage . && gh extension install .   # run your local build as `gh dep-triage`
```

Releases are built by [`cli/gh-extension-precompile`](https://github.com/cli/gh-extension-precompile) when a `v*` tag is pushed.
