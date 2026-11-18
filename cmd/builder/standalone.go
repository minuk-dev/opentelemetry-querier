package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/mod/module"
)

var errOutputConflict = errors.New("standalone output conflicts with an existing file")

func buildDistribution(outDir string, manifest *Manifest, data tmplData) error {
	requirements, err := standaloneRequirements(manifest, data)
	if err != nil {
		return err
	}

	binary := data.Command
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	files := []string{"go.mod", "go.sum", "main.go", "components.go", binary}
	header := fmt.Sprintf("%s\n// Binary: %s\n\nmodule %s\n", generated, binary, manifest.Dist.Module)

	err = checkOutput(outDir, header, files)
	if err != nil {
		return err
	}

	err = os.MkdirAll(outDir, dirPerm)
	if err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	stage, err := os.MkdirTemp(outDir, ".builder-")
	if err != nil {
		return fmt.Errorf("create build directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(stage) }()

	err = compileDistribution(stage, header, binary, requirements, data)
	if err != nil {
		return err
	}

	for _, name := range files {
		err = os.Rename(filepath.Join(stage, name), filepath.Join(outDir, name))
		if err != nil {
			return fmt.Errorf("publish %s: %w", name, err)
		}
	}

	return nil
}

func compileDistribution(stage, header, binary string, requirements []string, data tmplData) error {
	err := generate(stage, data)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(stage, "go.mod"), []byte(header+"\ngo 1.25.4\n\nrequire (\n"+
		strings.Join(requirements, "\n")+"\n)\n"), filePerm)
	if err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}

	// Download each requested version even if dependency selection later raises it.
	downloads := []string{"mod", "download"}
	for _, requirement := range requirements {
		downloads = append(downloads, strings.Replace(requirement, " ", "@", 1))
	}

	err = runGo(stage, "mod download", downloads...)
	if err != nil {
		return err
	}

	err = runGo(stage, "mod tidy", "mod", "tidy")
	if err != nil {
		return err
	}

	return runGo(stage, "build", "build", "-mod=readonly", "-o", binary, ".")
}

func standaloneRequirements(manifest *Manifest, data tmplData) ([]string, error) {
	err := module.CheckPath(manifest.Dist.Module)
	if err != nil {
		return nil, fmt.Errorf("dist.module: %w", err)
	}

	pins := map[string]string{}
	// Framework modules share the same release revision.
	for _, name := range []string{
		"acceptor", "component", "connector", "dispatcher", "gen",
		"pipeline", "processor", "qdata", "qerror", "querier",
	} {
		pins[corePath+"/"+name] = manifest.Dist.CoreVersion
	}

	for _, spec := range slices.Concat(data.Acceptors, data.Processors, data.Dispatchers, data.Connectors) {
		if _, exists := pins[spec.Import]; exists {
			return nil, fmt.Errorf("%w: component duplicates framework module %q", errManifest, spec.Import)
		}

		pins[spec.Import] = spec.Version
	}

	requirements := make([]string, 0, len(pins))
	for mod, version := range pins {
		if mod == manifest.Dist.Module {
			return nil, fmt.Errorf("%w: dist.module also appears as a dependency: %s", errManifest, mod)
		}

		if version == "" || module.CanonicalVersion(version) != version {
			return nil, fmt.Errorf("%w: %s requires a canonical version, got %q", errManifest, mod, version)
		}

		err := module.Check(mod, version)
		if err != nil {
			return nil, fmt.Errorf("invalid module requirement: %w", err)
		}

		requirements = append(requirements, mod+" "+version)
	}

	slices.Sort(requirements)

	return requirements, nil
}

func checkOutput(outDir, header string, files []string) error {
	owned := false
	for _, name := range files {
		path := filepath.Join(outDir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return fmt.Errorf("inspect output %s: %w", name, err)
		}

		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s", errOutputConflict, name)
		}

		if name == "go.mod" {
			err := checkModuleHeader(path, header)
			if err != nil {
				return err
			}

			owned = true
		}

		if !owned {
			return fmt.Errorf("%w: %s", errOutputConflict, name)
		}
	}

	return nil
}

func checkModuleHeader(path, header string) error {
	mod, err := os.ReadFile(path) //nolint:gosec // The caller checked this fixed output file is regular.
	if err != nil {
		return fmt.Errorf("read output go.mod: %w", err)
	}

	if !strings.HasPrefix(string(mod), header) {
		return fmt.Errorf("%w: go.mod belongs to a different distribution", errOutputConflict)
	}

	return nil
}

func runGo(dir, step string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), "go", args...) //nolint:gosec // Validated Go subcommand and module arguments.
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = append(os.Environ(), "GOWORK=off")

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("go %s: %w", step, err)
	}

	return nil
}
