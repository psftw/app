package render

import (
	"strings"

	"github.com/deislabs/cnab-go/bundle"
	"github.com/docker/app/internal/compose"
	"github.com/docker/app/types"
	"github.com/docker/app/types/parameters"
	"github.com/docker/cli/cli/compose/loader"
	composetemplate "github.com/docker/cli/cli/compose/template"
	composetypes "github.com/docker/cli/cli/compose/types"
	"github.com/pkg/errors"

	// Register json formatter
	_ "github.com/docker/app/internal/formatter/json"
	// Register yaml formatter
	_ "github.com/docker/app/internal/formatter/yaml"
)

// Render renders the Compose file for this app, merging in parameters files, other compose files, and env
// appname string, composeFiles []string, parametersFiles []string
func Render(app *types.App, env map[string]string, imageMap map[string]bundle.Image) (*composetypes.Config, error) {
	// prepend the app parameters to the argument parameters
	// load the parameters into a struct
	fileParameters := app.Parameters()
	// inject our metadata
	metaPrefixed, err := parameters.Load(app.MetadataRaw(), parameters.WithPrefix("app"))
	if err != nil {
		return nil, err
	}
	envParameters, err := parameters.FromFlatten(env)
	if err != nil {
		return nil, err
	}
	allParameters, err := parameters.Merge(fileParameters, metaPrefixed, envParameters)
	if err != nil {
		return nil, errors.Wrap(err, "failed to merge parameters")
	}
	configFiles, _, err := compose.Load(app.Composes())
	if err != nil {
		return nil, errors.Wrap(err, "failed to load composefiles")
	}
	return render(app.Path, configFiles, allParameters.Flatten(), imageMap)
}

func render(appPath string, configFiles []composetypes.ConfigFile, finalEnv map[string]string, imageMap map[string]bundle.Image) (*composetypes.Config, error) {
	rendered, err := loader.Load(composetypes.ConfigDetails{
		WorkingDir:  appPath,
		ConfigFiles: configFiles,
		Environment: finalEnv,
	}, func(opts *loader.Options) {
		opts.Interpolate.Substitute = substitute
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to load Compose file")
	}
	if err := processEnabled(rendered); err != nil {
		return nil, err
	}
	for ix, service := range rendered.Services {
		if img, ok := imageMap[service.Name]; ok {
			service.Image = img.Image
			rendered.Services[ix] = service
		}
	}
	return rendered, nil
}

func substitute(template string, mapping composetemplate.Mapping) (string, error) {
	return composetemplate.SubstituteWith(template, mapping, compose.ExtrapolationPattern, errorIfMissing)
}

func errorIfMissing(substitution string, mapping composetemplate.Mapping) (string, bool, error) {
	value, found := mapping(substitution)
	if !found {
		return "", true, &composetemplate.InvalidTemplateError{
			Template: "required variable " + substitution + " is missing a value",
		}
	}
	return value, true, nil
}

func processEnabled(config *composetypes.Config) error {
	services := []composetypes.ServiceConfig{}
	for _, service := range config.Services {
		if service.Extras != nil {
			if xEnabled, ok := service.Extras["x-enabled"]; ok {
				enabled, err := isEnabled(xEnabled)
				if err != nil {
					return err
				}
				if !enabled {
					continue
				}
			}
		}
		services = append(services, service)
	}
	config.Services = services
	return nil
}

func isEnabled(e interface{}) (bool, error) {
	switch v := e.(type) {
	case string:
		v = strings.ToLower(strings.TrimSpace(v))
		switch {
		case v == "1", v == "true":
			return true, nil
		case v == "", v == "0", v == "false":
			return false, nil
		case strings.HasPrefix(v, "!"):
			nv, err := isEnabled(v[1:])
			if err != nil {
				return false, err
			}
			return !nv, nil
		default:
			return false, errors.Errorf("%s is not a valid value for x-enabled", e)
		}
	case bool:
		return v, nil
	}
	return false, errors.Errorf("invalid type (%T) for x-enabled", e)
}
