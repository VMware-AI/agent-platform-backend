package catalog

import "strings"

// ResolvePlaceholders substitutes {{KEY}} tokens in s with vars[KEY]. Tokens with
// no matching var are left intact (a blanked install command would be a broken
// command that still looks runnable). Used to render an AgentTemplate's
// install_command for display/deploy (LLD-05 §1: 占位符服务端解析).
func ResolvePlaceholders(s string, vars map[string]string) string {
	if s == "" || len(vars) == 0 {
		return s
	}
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}
