#!/usr/bin/env bash
set -euo pipefail

repo=$(git rev-parse --show-toplevel)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tmp=$(cd "$tmp" && pwd -P)

core=github.com/minuk-dev/opentelemetry-querier
plugin=github.com/querier-builder-test/customprocessor
version=$(awk '/core_version:/ {print $2}' "$repo/examples/standalone/builder.yaml")

# Publish the candidate into an isolated Git repo for Go to fetch by version.
# The URL rewrite and sumdb exception apply only to these local fixtures.
git clone --quiet --no-local "$repo" "$tmp/modules"
(cd "$tmp/modules" && git checkout --quiet --detach "$(git -C "$repo" rev-parse HEAD)" &&
  scripts/tag-go-modules.sh "$version" >/dev/null)
git init --quiet "$tmp/plugin"
cat > "$tmp/plugin/go.mod" <<MOD
module $plugin

go 1.25.4

require (
    $core/component $version
    $core/processor $version
)
MOD
cat > "$tmp/plugin/processor.go" <<GO
package customprocessor
import (
    "context"
    "$core/component"
    "$core/processor"
)
func NewFactory() processor.Factory {
    return processor.NewFactory(component.MustNewType("custom"),
        func() component.Config { return &struct{}{} },
        func(context.Context, component.Settings, component.Config) (processor.Processor, error) {
            return processor.Base{}, nil
        })
}
GO
git -C "$tmp/plugin" add .
git -C "$tmp/plugin" -c user.name=fixture -c user.email=fixture@example.com commit --quiet -m fixture
git -C "$tmp/plugin" tag "$version"

export GIT_CONFIG_COUNT=2
export GIT_CONFIG_KEY_0="url.file://$tmp/modules.insteadOf" GIT_CONFIG_VALUE_0="https://$core"
export GIT_CONFIG_KEY_1="url.file://$tmp/plugin.insteadOf" GIT_CONFIG_VALUE_1="https://$plugin"
export GONOPROXY="$core,$plugin" GONOSUMDB="$core,$plugin"
export GOWORK=off GOBIN="$tmp/bin" GOMODCACHE="$tmp/modcache"
export GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org

mkdir "$tmp/consumer"
cd "$tmp/consumer"
go install "$core/cmd/builder@$version"
python3 "$repo/scripts/standalone-smoke.py" "$tmp/bin/builder" "$repo/examples/standalone" "$plugin@$version"
