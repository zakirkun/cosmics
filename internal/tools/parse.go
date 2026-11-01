package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/cosmics/cosmics/internal/domain"
)

// yamlTool mirrors Tool for YAML decoding.
//
// It exists because yaml.v3 cannot unmarshal a mapping into json.RawMessage:
// arg_schema is a nested mapping in the file and must be re-encoded as JSON after
// decoding. A separate type keeps Tool free of YAML-specific concerns.
type yamlTool struct {
	ID               string      `yaml:"id"`
	Category         Category    `yaml:"category"`
	DisplayName      string      `yaml:"display_name"`
	Image            string      `yaml:"image"`
	Binary           string      `yaml:"binary"`
	VersionCmd       []string    `yaml:"version_cmd"`
	ArgSchema        any         `yaml:"arg_schema"`
	CommandTemplate  string      `yaml:"command_template"`
	Capabilities     []string    `yaml:"capabilities"`
	NetworkMode      NetworkMode `yaml:"network_mode"`
	DefaultTimeoutS  int         `yaml:"default_timeout_s"`
	MaxOutputBytes   int         `yaml:"max_output_bytes"`
	DangerLevel      DangerLevel `yaml:"danger_level"`
	RequiresApproval bool        `yaml:"requires_approval"`
	Parser           Parser      `yaml:"parser"`
	FidelityCaveats  []string    `yaml:"fidelity_caveats"`
	Notes            string      `yaml:"notes"`
	Enabled          *bool       `yaml:"enabled"`
}

// parseTool decodes one tool definition from YAML bytes.
func parseTool(raw []byte) (Tool, error) {
	var decoded yamlTool
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		return Tool{}, err
	}

	schema, err := json.Marshal(decoded.ArgSchema)
	if err != nil {
		return Tool{}, fmt.Errorf("encode arg_schema: %w", err)
	}

	enabled := true
	if decoded.Enabled != nil {
		enabled = *decoded.Enabled
	}

	return Tool{
		ID:               decoded.ID,
		Category:         decoded.Category,
		DisplayName:      decoded.DisplayName,
		Image:            decoded.Image,
		Binary:           decoded.Binary,
		VersionCmd:       decoded.VersionCmd,
		ArgSchema:        schema,
		CommandTemplate:  decoded.CommandTemplate,
		Capabilities:     decoded.Capabilities,
		NetworkMode:      decoded.NetworkMode,
		DefaultTimeoutS:  decoded.DefaultTimeoutS,
		MaxOutputBytes:   decoded.MaxOutputBytes,
		DangerLevel:      decoded.DangerLevel,
		RequiresApproval: decoded.RequiresApproval,
		Parser:           decoded.Parser,
		FidelityCaveats:  decoded.FidelityCaveats,
		Notes:            decoded.Notes,
		Enabled:          enabled,
	}, nil
}

// extractSchema is unused now that parseTool handles re-encoding; it remains as
// the documented fallback path for a definition whose schema failed to decode.
func extractSchema(raw []byte) (json.RawMessage, error) {
	var decoded yamlTool
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(decoded.ArgSchema)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// sortByID orders tools deterministically.
func sortByID(tools []Tool) {
	sort.Slice(tools, func(i, j int) bool { return tools[i].ID < tools[j].ID })
}

// ErrEmptyRegistry is returned when no tool definition could be loaded.
var ErrEmptyRegistry = errors.New("tools: registry is empty")

// defaultStageCategories is the stage allow-list from CORE.md section 5.2.
var defaultStageCategories = map[domain.StageKind][]Category{
	domain.StageRecon:     {CategoryRecon, CategoryNetwork, CategoryWeb},
	domain.StageOSINT:     {CategoryOSINT},
	domain.StageAnalysis:  nil,
	domain.StageMapper:    nil,
	domain.StageExploit:   {CategoryExploit, CategoryWeb},
	domain.StageReporting: nil,
}
