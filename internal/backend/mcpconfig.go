package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MCPServerConfig from claude.json / settings.json
type MCPServerConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// LoadMCPServerNames reads all configured MCP server command names from
// Claude Code config files. Returns set of command basenames.
func LoadMCPServerNames(projectDir string) map[string]bool {
	names := make(map[string]bool)

	home, _ := os.UserHomeDir()
	if home == "" {
		return names
	}

	// Sources in priority order
	paths := []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", "settings.json"),
	}
	if projectDir != "" {
		paths = append(paths,
			filepath.Join(projectDir, ".mcp.json"),
			filepath.Join(projectDir, ".claude", "settings.json"),
		)
		// Project-specific config dir
		encoded := encodeMCPPath(projectDir)
		paths = append(paths, filepath.Join(home, ".claude", "projects", encoded, "settings.json"))
	}

	for _, p := range paths {
		extractMCPNames(p, names)
	}

	return names
}

func extractMCPNames(path string, names map[string]bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var doc struct {
		MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return
	}

	for serverName, cfg := range doc.MCPServers {
		// Add server name itself
		names[serverName] = true
		// Add command basename
		if cfg.Command != "" {
			names[filepath.Base(cfg.Command)] = true
			// Common launchers: npx, uvx, node — add first arg too
			base := filepath.Base(cfg.Command)
			if (base == "npx" || base == "uvx" || base == "node" || base == "python3" || base == "python") && len(cfg.Args) > 0 {
				names[filepath.Base(cfg.Args[0])] = true
			}
		}
	}
}

func encodeMCPPath(path string) string {
	result := make([]byte, 0, len(path))
	for _, c := range []byte(path) {
		if c == '/' {
			result = append(result, '-')
		} else {
			result = append(result, c)
		}
	}
	return string(result)
}
