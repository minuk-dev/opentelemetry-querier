package main

import (
	"fmt"
	"go/token"
	"path"
	"regexp"
	"strings"

	"golang.org/x/mod/module"
)

const (
	corePath            = "github.com/minuk-dev/opentelemetry-querier"
	moduleVersionFields = 2
)

var binaryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func prepare(manifest *Manifest) (tmplData, error) {
	data := tmplData{
		Version:   orDefault(manifest.Dist.Version, "dev"),
		Command:   orDefault(manifest.Dist.Name, "querier"),
		Acceptors: nil, Processors: nil, Dispatchers: nil, Connectors: nil,
	}
	if !binaryName.MatchString(data.Command) {
		return data, fmt.Errorf("%w: invalid dist.name %q", errManifest, data.Command)
	}

	paths := map[string]bool{}
	aliases := map[string]bool{
		"acceptor": true, "connector": true, "dispatcher": true, "processor": true, "querier": true,
	}

	for _, group := range []struct {
		components []Component
		output     *[]tmplComponent
	}{
		{manifest.Acceptors, &data.Acceptors},
		{manifest.Processors, &data.Processors},
		{manifest.Dispatchers, &data.Dispatchers},
		{manifest.Connectors, &data.Connectors},
	} {
		for _, component := range group.components {
			spec, err := parseComponent(component.GoMod)
			if err != nil {
				return data, err
			}

			if paths[spec.Import] || aliases[spec.Alias] {
				return data, fmt.Errorf("%w: duplicate component or import alias for %q (%s)",
					errManifest, spec.Import, spec.Alias)
			}

			paths[spec.Import] = true
			aliases[spec.Alias] = true
			*group.output = append(*group.output, spec)
		}
	}

	return data, nil
}

func parseComponent(value string) (tmplComponent, error) {
	var spec tmplComponent

	fields := strings.Fields(value)
	switch len(fields) {
	case 1:
		spec.Import, spec.Version, _ = strings.Cut(fields[0], "@")
		if strings.HasSuffix(fields[0], "@") {
			return spec, fmt.Errorf("%w: empty version in gomod %q", errManifest, value)
		}
	case moduleVersionFields:
		spec.Import, spec.Version = fields[0], fields[1]
	default:
		return spec, fmt.Errorf("%w: expected module[@version] in gomod %q", errManifest, value)
	}

	err := module.CheckImportPath(spec.Import)
	if err != nil {
		return spec, fmt.Errorf("gomod %q: %w", value, err)
	}

	spec.Alias = strings.NewReplacer("-", "", ".", "").Replace(path.Base(spec.Import))
	if !token.IsIdentifier(spec.Alias) || spec.Alias == "_" {
		return spec, fmt.Errorf("%w: invalid import alias %q for %q", errManifest, spec.Alias, spec.Import)
	}

	return spec, nil
}
