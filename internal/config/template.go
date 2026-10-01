package config

import (
	"fmt"
	"strings"
	"text/template"
)

// templateCache keeps parsed templates so execution-time rendering does not re-parse them.
// It is populated during config initialization and read-only afterwards.
type templateCache struct {
	templates map[string]*template.Template
}

func newTemplateCache() *templateCache {
	return &templateCache{templates: make(map[string]*template.Template)}
}

func (c *templateCache) prepare(data RoleCommandTemplateData, command string, args []string, env map[string]string) error {
	if err := c.prepareOne(data, "command", command); err != nil {
		return err
	}
	for i, arg := range args {
		if err := c.prepareOne(data, fmt.Sprintf("args[%d]", i), arg); err != nil {
			return err
		}
	}
	for key, value := range env {
		if err := c.prepareOne(data, fmt.Sprintf("env[%s]", key), value); err != nil {
			return err
		}
	}
	return nil
}

func (c *templateCache) prepareOne(data RoleCommandTemplateData, field, source string) error {
	if _, ok := c.templates[source]; ok {
		return nil
	}
	tmpl, err := template.New(field).Parse(source)
	if err != nil {
		return fmt.Errorf("failed to parse %s template: %w", field, err)
	}
	if _, err := executeTemplate(tmpl, data); err != nil {
		return fmt.Errorf("failed to execute %s template: %w", field, err)
	}
	c.templates[source] = tmpl
	return nil
}

func (c *templateCache) render(data RoleCommandTemplateData, source string) (string, error) {
	tmpl, ok := c.templates[source]
	if !ok {
		var err error
		tmpl, err = template.New("command").Parse(source)
		if err != nil {
			return "", fmt.Errorf("failed to parse command template: %w", err)
		}
	}
	return executeTemplate(tmpl, data)
}

func executeTemplate(tmpl *template.Template, data RoleCommandTemplateData) (string, error) {
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
