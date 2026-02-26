package logging

import "github.com/charmbracelet/log"

// New returns a logger whose prefix is "<nodeName> <component>" when nodeName is non-empty,
// or just "<component>" otherwise. Use this everywhere a *log.Logger is constructed so that
// the originating node name appears consistently in every log line.
func New(nodeName, component string) *log.Logger {
	if nodeName == "" {
		return log.WithPrefix(component)
	}
	return log.WithPrefix(nodeName + " " + component)
}
