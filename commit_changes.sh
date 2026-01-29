#!/bin/bash
set -e

cd /Users/laurenleach/go/src/github.com/ConductorOne/baton-lucidchart

# Stage all changes
git add -A

# Remove old config files
git rm cmd/baton-lucidchart/config.go cmd/baton-lucidchart/config_test.go

# Commit with proper message
git commit -m "$(cat <<'EOF'
Add containerization support

- Move config from cmd/ to pkg/config/ package
- Create generated config schema using baton-sdk config.Generate
- Update main.go to use new config pattern
- Update Makefile to generate config and support build tags
- Update CI workflow to use go-version-file and run on main branch
- Update capabilities workflow to generate config schema
- Remove lambda flag from release workflow
- Delete old cmd/baton-lucidchart/config.go and config_test.go

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>
EOF
)"

# Push the branch
git push -u origin containerize

echo "Successfully committed and pushed changes!"
