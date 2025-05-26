package log

import (
	"github.com/goccy/go-yaml"
)

func PrettyYaml(data any) ([]byte, error) {
	return yaml.MarshalWithOptions(data, yaml.Indent(2))
}

func PrettyYamlString(data any) string {
	prettyData, err := PrettyYaml(data)
	if err != nil {
		return ""
	}
	return string(prettyData)
}
