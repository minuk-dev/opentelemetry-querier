// Command builder is a small analogue of the opentelemetry-collector-builder
// (ocb). It reads a builder.yaml manifest listing the acceptor / processor /
// dispatcher components to include, and generates the distribution's entrypoint
// (main.go) and factory registration (components.go) under dist.output_path.
//
// This realizes the two-layer model: builder.yaml selects components at build
// time; the generated distribution then reads a runtime config.yaml to
// instantiate and wire them into pipelines.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/go-viper/mapstructure/v2"
	"gopkg.in/yaml.v3"
)

// errNoModule is returned when the manifest omits dist.module.
var errNoModule = errors.New("builder: manifest dist.module is required")

const (
	// dirPerm is the permission for generated output directories.
	dirPerm = 0o750
	// filePerm is the permission for generated files.
	filePerm = 0o600
)

// Manifest is the builder.yaml schema.
type Manifest struct {
	Dist struct {
		Name        string `mapstructure:"name"`
		Description string `mapstructure:"description"`
		Module      string `mapstructure:"module"`
		Version     string `mapstructure:"version"`
		OutputPath  string `mapstructure:"output_path"`
	} `mapstructure:"dist"`
	Acceptors   []Component `mapstructure:"acceptors"`
	Processors  []Component `mapstructure:"processors"`
	Dispatchers []Component `mapstructure:"dispatchers"`
}

// Component is one selected component, identified by its Go module/import path.
type Component struct {
	GoMod string `mapstructure:"gomod"`
}

// tmplComponent is the view passed to templates.
type tmplComponent struct {
	Alias  string // import alias, e.g. "otqp"
	Import string // import path (version stripped)
}

type tmplData struct {
	Module      string
	Version     string
	Command     string
	Acceptors   []tmplComponent
	Processors  []tmplComponent
	Dispatchers []tmplComponent
}

func main() {
	configPath := flag.String("config", "builder.yaml", "path to the builder manifest")

	flag.Parse()

	manifest, err := loadManifest(*configPath)
	if err != nil {
		log.Fatalf("builder: %v", err)
	}

	data := tmplData{
		Module:      manifest.Dist.Module,
		Version:     orDefault(manifest.Dist.Version, "dev"),
		Command:     orDefault(manifest.Dist.Name, "querier"),
		Acceptors:   toTmpl(manifest.Acceptors),
		Processors:  toTmpl(manifest.Processors),
		Dispatchers: toTmpl(manifest.Dispatchers),
	}

	outDir := manifest.Dist.OutputPath
	if outDir == "" {
		outDir = "./cmd/querier"
	}

	err = os.MkdirAll(outDir, dirPerm)
	if err != nil {
		log.Fatalf("builder: mkdir %s: %v", outDir, err)
	}

	templates := map[string]*template.Template{
		"components.go": componentsTemplate(),
		"main.go":       mainTemplate(),
	}

	for name, tmpl := range templates {
		err = render(filepath.Join(outDir, name), tmpl, data)
		if err != nil {
			log.Fatalf("builder: generate %s: %v", name, err)
		}
	}

	log.Printf("builder: generated distribution %q in %s", data.Command, outDir)
}

func loadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}

	var tree map[string]any

	err = yaml.Unmarshal(raw, &tree)
	if err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}

	var manifest Manifest

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:      &manifest,
		ErrorUnused: true,
		TagName:     "mapstructure",
	})
	if err != nil {
		return nil, fmt.Errorf("builder: build decoder: %w", err)
	}

	err = decoder.Decode(tree)
	if err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", path, err)
	}

	if manifest.Dist.Module == "" {
		return nil, errNoModule
	}

	return &manifest, nil
}

// toTmpl derives an import alias (the last path element, sanitized) for each
// component, stripping any "@version" suffix from the gomod path.
func toTmpl(components []Component) []tmplComponent {
	out := make([]tmplComponent, 0, len(components))

	for _, comp := range components {
		imp := comp.GoMod
		if at := strings.IndexByte(imp, '@'); at >= 0 {
			imp = imp[:at]
		}

		if sp := strings.IndexByte(imp, ' '); sp >= 0 {
			imp = imp[:sp]
		}

		out = append(out, tmplComponent{Alias: sanitizeAlias(path.Base(imp)), Import: imp})
	}

	return out
}

func sanitizeAlias(name string) string {
	var builder strings.Builder

	for _, char := range name {
		if char == '-' || char == '.' {
			continue
		}

		builder.WriteRune(char)
	}

	return builder.String()
}

func render(outPath string, tmpl *template.Template, data tmplData) error {
	var buf bytes.Buffer

	err := tmpl.Execute(&buf, data)
	if err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("gofmt: %w\n%s", err, buf.String())
	}

	err = os.WriteFile(outPath, formatted, filePerm)
	if err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	return nil
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func componentsTemplate() *template.Template {
	return template.Must(template.New("components").Parse(componentsTmpl))
}

func mainTemplate() *template.Template {
	return template.Must(template.New("main").Parse(mainTmpl))
}

const componentsTmpl = `// Code generated by "cmd/builder"; DO NOT EDIT.

package main

import (
	"{{ .Module }}/acceptor"
	"{{ .Module }}/dispatcher"
	"{{ .Module }}/processor"
	"{{ .Module }}/querier"
{{- range .Acceptors }}
	{{ .Alias }} "{{ .Import }}"
{{- end }}
{{- range .Processors }}
	{{ .Alias }} "{{ .Import }}"
{{- end }}
{{- range .Dispatchers }}
	{{ .Alias }} "{{ .Import }}"
{{- end }}
)

// components registers the factories selected in builder.yaml.
func components() (querier.Factories, error) {
	var factories querier.Factories
	var err error

	if factories.Acceptors, err = acceptor.MakeFactoryMap(
{{- range .Acceptors }}
		{{ .Alias }}.NewFactory(),
{{- end }}
	); err != nil {
		return factories, err
	}

	if factories.Processors, err = processor.MakeFactoryMap(
{{- range .Processors }}
		{{ .Alias }}.NewFactory(),
{{- end }}
	); err != nil {
		return factories, err
	}

	if factories.Dispatchers, err = dispatcher.MakeFactoryMap(
{{- range .Dispatchers }}
		{{ .Alias }}.NewFactory(),
{{- end }}
	); err != nil {
		return factories, err
	}

	return factories, nil
}
`

const mainTmpl = `// Code generated by "cmd/builder"; DO NOT EDIT.

// Command {{ .Command }} is the generated OpenTelemetry Querier distribution
// entrypoint. It loads a runtime config, assembles the pipelines from the
// compiled-in factories (see components.go), and runs them.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"{{ .Module }}/component"
	"{{ .Module }}/querier"
)

// version is the distribution version. The builder manifest's dist.version
// seeds it, but release builds override it at link time:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/{{ .Command }}
var version = "{{ .Version }}"

var buildInfo = component.BuildInfo{Command: "{{ .Command }}", Version: version}

func main() {
	configPath := flag.String("config", "config.yaml", "path to the runtime config file")
	flag.Parse()

	cfg, err := querier.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("{{ .Command }}: %v", err)
	}

	factories, err := components()
	if err != nil {
		log.Fatalf("{{ .Command }}: %v", err)
	}

	svc, err := querier.Build(factories, cfg, buildInfo)
	if err != nil {
		log.Fatalf("{{ .Command }}: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := svc.Start(ctx); err != nil {
		log.Fatalf("{{ .Command }}: %v", err)
	}
	log.Printf("{{ .Command }}: started with config %s", *configPath)

	<-ctx.Done()
	log.Printf("{{ .Command }}: shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Shutdown(shutdownCtx); err != nil {
		log.Printf("{{ .Command }}: shutdown error: %v", err)
		os.Exit(1)
	}
}
`
