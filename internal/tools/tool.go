// Package tools owns the tool registry: the typed catalog of external programs
// the agent may run, and the argv construction that keeps them safe to invoke.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for registry and invocation failures.
var (
	ErrInvalidTool    = errors.New("tools: invalid tool definition")
	ErrToolNotFound   = errors.New("tools: tool not found")
	ErrInvalidArgs    = errors.New("tools: invalid arguments")
	ErrInvalidArgv    = errors.New("tools: invalid argv")
	ErrToolDisabled   = errors.New("tools: tool is disabled")
	ErrStageForbidden = errors.New("tools: tool not permitted in this stage")
)

// Category groups tools by the work they do.
type Category string

// Tool categories, matching the tool_defs CHECK constraint.
const (
	CategoryRecon   Category = "recon"
	CategoryOSINT   Category = "osint"
	CategoryWeb     Category = "web"
	CategoryNetwork Category = "network"
	CategoryExploit Category = "exploit"
	CategoryPost    Category = "post"
	CategoryReport  Category = "report"
)

// Valid reports whether the category is declared.
func (c Category) Valid() bool {
	switch c {
	case CategoryRecon, CategoryOSINT, CategoryWeb, CategoryNetwork,
		CategoryExploit, CategoryPost, CategoryReport:
		return true
	}
	return false
}

// DangerLevel states how much harm a tool can do.
type DangerLevel string

// Danger levels, ordered by increasing risk.
const (
	DangerSafe        DangerLevel = "safe"
	DangerIntrusive   DangerLevel = "intrusive"
	DangerDestructive DangerLevel = "destructive"
)

// Valid reports whether the level is declared.
func (d DangerLevel) Valid() bool {
	switch d {
	case DangerSafe, DangerIntrusive, DangerDestructive:
		return true
	}
	return false
}

// NetworkMode is the container network policy for a tool.
type NetworkMode string

// Network modes.
const (
	NetworkBridge NetworkMode = "bridge"
	NetworkHost   NetworkMode = "host"
	NetworkNone   NetworkMode = "none"
)

// Valid reports whether the mode is declared.
func (n NetworkMode) Valid() bool {
	switch n {
	case NetworkBridge, NetworkHost, NetworkNone:
		return true
	}
	return false
}

// Parser names the output decoder for a tool.
type Parser string

// Parsers.
const (
	ParserNmapXML Parser = "nmap_xml"
	ParserJSON    Parser = "json"
	ParserJSONL   Parser = "jsonl"
	ParserLines   Parser = "lines"
	ParserNone    Parser = "none"
)

// Valid reports whether the parser is declared.
func (p Parser) Valid() bool {
	switch p {
	case ParserNmapXML, ParserJSON, ParserJSONL, ParserLines, ParserNone:
		return true
	}
	return false
}

// Tool is one executable capability.
type Tool struct {
	ID               string          `yaml:"id" json:"id"`
	Category         Category        `yaml:"category" json:"category"`
	DisplayName      string          `yaml:"display_name" json:"display_name"`
	Image            string          `yaml:"image" json:"image"`
	Binary           string          `yaml:"binary" json:"binary"`
	VersionCmd       []string        `yaml:"version_cmd" json:"version_cmd"`
	ArgSchema        json.RawMessage `yaml:"arg_schema" json:"arg_schema"`
	CommandTemplate  string          `yaml:"command_template" json:"command_template"`
	Capabilities     []string        `yaml:"capabilities" json:"capabilities"`
	NetworkMode      NetworkMode     `yaml:"network_mode" json:"network_mode"`
	DefaultTimeoutS  int             `yaml:"default_timeout_s" json:"default_timeout_s"`
	MaxOutputBytes   int             `yaml:"max_output_bytes" json:"max_output_bytes"`
	DangerLevel      DangerLevel     `yaml:"danger_level" json:"danger_level"`
	RequiresApproval bool            `yaml:"requires_approval" json:"requires_approval"`
	Parser           Parser          `yaml:"parser" json:"parser"`
	FidelityCaveats  []string        `yaml:"fidelity_caveats" json:"fidelity_caveats"`
	Notes            string          `yaml:"notes" json:"notes"`
	Enabled          bool            `yaml:"enabled" json:"enabled"`
}

// Validate checks every field and reports all problems at once.
func (t Tool) Validate() error {
	var problems []error

	if strings.TrimSpace(t.ID) == "" {
		problems = append(problems, errors.New("id must not be empty"))
	}
	if !t.Category.Valid() {
		problems = append(problems, fmt.Errorf("category %q is not declared", t.Category))
	}
	if strings.TrimSpace(t.DisplayName) == "" {
		problems = append(problems, errors.New("display_name must not be empty"))
	}
	if strings.TrimSpace(t.Image) == "" {
		problems = append(problems, errors.New("image must not be empty"))
	}
	if strings.TrimSpace(t.Binary) == "" {
		problems = append(problems, errors.New("binary must not be empty"))
	}
	if len(t.ArgSchema) == 0 {
		problems = append(problems, errors.New("arg_schema must not be empty"))
	} else if !json.Valid(t.ArgSchema) {
		problems = append(problems, errors.New("arg_schema is not valid JSON"))
	}
	if strings.TrimSpace(t.CommandTemplate) == "" {
		problems = append(problems, errors.New("command_template must not be empty"))
	} else if err := validateCommandTemplate(t.CommandTemplate); err != nil {
		problems = append(problems, err)
	}
	if !t.NetworkMode.Valid() {
		problems = append(problems, fmt.Errorf("network_mode %q is not declared", t.NetworkMode))
	}
	if t.DefaultTimeoutS <= 0 {
		problems = append(problems, fmt.Errorf("default_timeout_s %d must be positive", t.DefaultTimeoutS))
	}
	if t.MaxOutputBytes <= 0 {
		problems = append(problems, fmt.Errorf("max_output_bytes %d must be positive", t.MaxOutputBytes))
	}
	if !t.DangerLevel.Valid() {
		problems = append(problems, fmt.Errorf("danger_level %q is not declared", t.DangerLevel))
	}
	if !t.Parser.Valid() {
		problems = append(problems, fmt.Errorf("parser %q is not declared", t.Parser))
	}

	if len(problems) > 0 {
		return fmt.Errorf("tool %q: %w: %w", t.ID, ErrInvalidTool, errors.Join(problems...))
	}
	return nil
}

// RequiresExploitationMode reports whether the tool may only run when the
// engagement mode permits exploitation.
func (t Tool) RequiresExploitationMode() bool {
	return t.Category == CategoryExploit || t.Category == CategoryPost
}
