# Sourced by demo.tape: builds lopper and a throwaway home in .tmp/demo with a
# few repositories and the worktrees agents leave behind, so the recording
# never shows the real disk.

set -e
root=$PWD
go build -o "$root/bin/lopper" ./cmd/lopper
rm -rf "$root/.tmp/demo" && mkdir -p "$root/.tmp/demo/code"

export HOME=$root/.tmp/demo PATH=$root/bin:$PATH LOPPER_NO_UPDATE_CHECK=1
export GIT_AUTHOR_NAME=demo GIT_AUTHOR_EMAIL=demo@example.com
export GIT_COMMITTER_NAME=demo GIT_COMMITTER_EMAIL=demo@example.com

# repo name: creates ~/code/name with a main branch that ignores node_modules.
repo() {
	git init -q -b main ~/code/"$1"
	echo node_modules/ >~/code/"$1"/.gitignore
	git -C ~/code/"$1" add .gitignore
	git -C ~/code/"$1" commit -q -m "chore: initialize repository"
}

# worktree repo path branch megabytes message: adds a worktree with one
# commit and an ignored node_modules of the given size.
worktree() {
	git -C ~/code/"$1" worktree add -q -b "$3" "$2"
	git -C ~/code/"$1"/"$2" commit -q --allow-empty -m "$5"
	mkdir -p ~/code/"$1"/"$2"/node_modules
	head -c $(($4 * 1024 * 1024)) /dev/zero >~/code/"$1"/"$2"/node_modules/blob
}

# merged repo branch: merges branch into main, as a finished PR would be.
merged() { git -C ~/code/"$1" merge -q --no-edit "$2"; }

repo storefront
worktree storefront ../storefront-web-142 feat/WEB-142-checkout 412 "feat(checkout): save carts between visits"
worktree storefront .codex/worktrees/web-158-search feat/WEB-158-search 398 "feat(search): filter products by category"
worktree storefront .claude/worktrees/web-163-dark-mode fix/WEB-163-dark-mode 356 "fix(theme): keep checkout readable in dark mode"
merged storefront feat/WEB-142-checkout
merged storefront fix/WEB-163-dark-mode

repo billing-api
worktree billing-api .claude/worktrees/api-87-token-refresh fix/API-87-token-refresh 184 "fix(auth): renew expired access tokens"
worktree billing-api .claude/worktrees/api-93-rate-limit feat/API-93-rate-limit 176 "feat(api): limit requests per account"
printf 'package middleware\n\nconst requestsPerMinute = 120\n' >~/code/billing-api/.claude/worktrees/api-93-rate-limit/limiter.go
merged billing-api fix/API-87-token-refresh

repo mobile-app
worktree mobile-app .claude/worktrees/mob-204-onboarding feat/MOB-204-onboarding 520 "feat(onboarding): resume account setup after restart"
worktree mobile-app .codex/worktrees/mob-219-push feat/MOB-219-push 505 "feat(notifications): register devices for push messages"
printf 'export const notificationPreferences = { orders: true };\n' >~/code/mobile-app/.codex/worktrees/mob-219-push/notify.ts
merged mobile-app feat/MOB-204-onboarding

repo developer-docs
worktree developer-docs .claude/worktrees/doc-42-install-guide fix/DOC-42-install-guide 64 "fix(docs): correct the macOS install command"
worktree developer-docs .claude/worktrees/doc-51-api-reference docs/DOC-51-api-reference 72 "docs(api): describe invoice pagination"
merged developer-docs fix/DOC-42-install-guide

repo platform-infra
worktree platform-infra ../platform-infra-ops-76 chore/OPS-76-terraform 48 "chore(terraform): update the AWS provider"
merged platform-infra chore/OPS-76-terraform

repo release-cli
worktree release-cli .claude/worktrees/cli-31-deps chore/CLI-31-deps 96 "chore(deps): update command dependencies"
merged release-cli chore/CLI-31-deps

set +e
cd ~
clear
