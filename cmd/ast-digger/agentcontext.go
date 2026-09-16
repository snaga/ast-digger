package main

import (
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// agentContextSchemaVersion is the schema_version reported by `agent-context`.
// It is bumped only when the shape of AgentContext itself changes (adding or
// removing top-level keys, or changing a field's meaning) -- not when
// commands/flags are added to the CLI, since those are reflected
// automatically by buildAgentContext.
const agentContextSchemaVersion = "1.0"

// AgentContext is the JSON document printed by `agent-context`: a
// machine-readable description of every registered subcommand's arguments
// and options, for an AI agent to self-inspect the CLI's capabilities. It
// corresponds to Python's `get_agent_context` return value in
// ast_digger/outline.py, but -- per design_GOLANG.md's F-6 -- Commands is
// populated by walking the actual *cobra.Command tree (buildAgentContext)
// rather than being hand-maintained, which is what structurally prevents the
// schema/implementation drift that requirement H-1 documented against the
// Python implementation (most notably, the persistent --json flag showing up
// on every single command below, including agent-context itself, without
// anyone having to remember to list it per command).
type AgentContext struct {
	SchemaVersion       string                   `json:"schema_version"`
	CLIName             string                   `json:"cli_name"`
	SupportedLanguages  []string                 `json:"supported_languages"`
	SupportedExtensions []string                 `json:"supported_extensions"`
	Commands            map[string]CommandSchema `json:"commands"`
}

// CommandSchema describes one subcommand's argument/option surface.
type CommandSchema struct {
	Description string           `json:"description"`
	Arguments   []ArgumentSchema `json:"arguments"`
	Options     []OptionSchema   `json:"options"`
}

// ArgumentSchema describes one positional argument, derived from a
// cobra.Command's Use string (see parseArguments): "<name>" tokens are
// required, "[name]" tokens are optional.
type ArgumentSchema struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  string `json:"default,omitempty"`
}

// OptionSchema describes one flag -- local to the command or inherited from
// an ancestor's persistent flag set (e.g. the root command's --json/-j) --
// derived from its pflag.Flag definition (see buildOptions).
type OptionSchema struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

// argTokenRegexp matches a single "<required_name>" or "[optional_name]"
// token in a cobra.Command's Use string.
var argTokenRegexp = regexp.MustCompile(`^(<|\[)([A-Za-z0-9_-]+)(>|\])$`)

// knownArgDefaults documents the substituted default value for optional
// positional arguments whose RunE falls back to a non-empty default when the
// argument is omitted (currently just `directory [dir_path]`, which defaults
// to "."). A cobra.Command's Use string and Args validator can express
// *that* an argument is optional, but not *what* it defaults to at runtime,
// so -- unlike every other field on ArgumentSchema/OptionSchema/CommandSchema
// -- this one small piece of information is kept as an explicit, static
// lookup rather than derived dynamically. It intentionally only ever adds
// the optional "default" key and never affects "required"/argument presence,
// so it cannot reintroduce the kind of schema/implementation drift task 13
// exists to prevent.
var knownArgDefaults = map[string]string{
	"dir_path": ".",
}

// buildAgentContext derives the full agent-context schema by walking every
// subcommand registered on rootCmd (via Commands()), including agent-context
// itself (described the same way as any other command, with its own
// -- empty -- argument list and its inherited --json option; there is no
// self-referential recursion here, buildCommandSchema only ever reads a
// *cobra.Command's static Use/Short/flag definitions). It corresponds to the
// dynamic-derivation design called for by design_GOLANG.md's F-6 and
// requirement H-1.
func buildAgentContext(rootCmd *cobra.Command) AgentContext {
	commands := make(map[string]CommandSchema)
	for _, cmd := range rootCmd.Commands() {
		if isCobraBuiltinCommand(cmd) {
			continue
		}
		commands[cmd.Name()] = buildCommandSchema(cmd)
	}

	return AgentContext{
		SchemaVersion:       agentContextSchemaVersion,
		CLIName:             rootCmd.Name(),
		SupportedLanguages:  []string{"python", "typescript", "golang", "java", "javascript", "rust", "sql", "markdown"},
		SupportedExtensions: []string{".py", ".ts", ".tsx", ".go", ".java", ".js", ".jsx", ".rs", ".sql", ".md", ".markdown"},
		Commands:            commands,
	}
}

// isCobraBuiltinCommand reports whether cmd is one of cobra's own
// automatically-injected utility commands (help, completion, and the hidden
// __complete command used for shell completion), rather than one of
// ast-digger's own subcommands. cobra lazily adds these to the root command
// the first time Execute/ExecuteC runs -- which, notably, is also how
// agent-context itself gets invoked -- so without this filter, whether
// "help"/"completion" show up in agent-context's own output would depend on
// incidental details of how/when the process was invoked, rather than
// describing a fixed, deterministic surface. Excluding them by their
// cobra-owned Hidden flag and well-known names (not any of ast-digger's own
// command/flag names) keeps agent-context's output limited to the CLI's own
// commands without reintroducing hand-curation of *those*.
func isCobraBuiltinCommand(cmd *cobra.Command) bool {
	if cmd.Hidden {
		return true
	}
	switch cmd.Name() {
	case "help", "completion":
		return true
	default:
		return false
	}
}

// buildCommandSchema derives cmd's CommandSchema from its Short description,
// the positional arguments encoded in its Use string (parseArguments), and
// its local + inherited flag definitions (buildOptions).
func buildCommandSchema(cmd *cobra.Command) CommandSchema {
	return CommandSchema{
		Description: cmd.Short,
		Arguments:   parseArguments(cmd.Use),
		Options:     buildOptions(cmd),
	}
}

// parseArguments extracts the positional-argument schema encoded in a
// cobra.Command's Use string, e.g. "symbol <file_path> <symbol_path>" ->
// [{file_path, required}, {symbol_path, required}], or
// "directory [dir_path]" -> [{dir_path, optional, default "."}]. Every
// whitespace-separated token after the command name that matches
// "<name>" or "[name]" becomes an ArgumentSchema, in the order it appears;
// any other token (there are none in this CLI's current Use strings) is
// ignored rather than causing an error, so a future command whose Use string
// doesn't follow this convention degrades gracefully to an empty argument
// list instead of panicking agent-context.
func parseArguments(use string) []ArgumentSchema {
	fields := strings.Fields(use)
	arguments := []ArgumentSchema{}
	if len(fields) <= 1 {
		return arguments
	}

	for _, tok := range fields[1:] {
		m := argTokenRegexp.FindStringSubmatch(tok)
		if m == nil {
			continue
		}
		name := m[2]
		required := m[1] == "<"

		arg := ArgumentSchema{Name: name, Type: "string", Required: required}
		if !required {
			if def, ok := knownArgDefaults[name]; ok {
				arg.Default = def
			}
		}
		arguments = append(arguments, arg)
	}
	return arguments
}

// buildOptions derives cmd's option schema from its own flags (LocalFlags:
// flags declared directly on cmd, e.g. `outline`'s --no-truncate) plus every
// flag it inherits from an ancestor's persistent flag set (InheritedFlags:
// e.g. the root command's --json/-j). Using LocalFlags/InheritedFlags rather
// than the raw Flags()/PersistentFlags() accessors guarantees the two sets
// are disjoint (a flag is either declared locally or inherited, never both),
// so no flag is ever listed twice; walking InheritedFlags for every command
// (not just ones that happen to declare it locally) is what makes --json
// show up in every single command's Options below, closing the H-1 gap the
// Python implementation had.
func buildOptions(cmd *cobra.Command) []OptionSchema {
	options := []OptionSchema{}

	collect := func(f *pflag.Flag) {
		// cobra lazily adds its own --help/-h flag to whichever command is
		// actually being executed (via InitDefaultHelpFlag, invoked right
		// before that command's RunE runs) -- which, again, includes
		// agent-context itself when it is the one running. Since that would
		// make --help appear in a command's schema only when that
		// particular command happened to have been the one invoked this
		// process, it is excluded here for every command, the same way
		// isCobraBuiltinCommand excludes cobra's own "help"/"completion"
		// commands: this is filtering out cobra-framework noise, not
		// hand-curating any of ast-digger's own flags.
		if f.Name == "help" {
			return
		}
		options = append(options, OptionSchema{
			Name:        "--" + f.Name,
			Type:        flagJSONType(f.Value.Type()),
			Default:     f.DefValue,
			Description: f.Usage,
		})
	}

	cmd.LocalFlags().VisitAll(collect)
	cmd.InheritedFlags().VisitAll(collect)

	return options
}

// flagJSONType maps a pflag.Value.Type() name (e.g. "bool", "int", "string")
// to the JSON schema type name used in OptionSchema.Type.
func flagJSONType(pflagType string) string {
	switch pflagType {
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return "integer"
	default:
		return "string"
	}
}

// newAgentContextCmd builds the `agent-context` subcommand: it prints
// buildAgentContext(cmd.Root())'s schema, always as JSON (matching Python's
// `agent-context` command, which calls `output_result(ctx, json_out=True)`
// unconditionally rather than branching on a --json flag). It corresponds to
// Python's `agent-context` command in ast_digger/__main__.py.
func newAgentContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent-context",
		Short: "Print machine-readable CLI schema context JSON for AI agents to self-inspect capabilities.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := buildAgentContext(cmd.Root())
			return outputResult(cmd.OutOrStdout(), ctx, true)
		},
	}
}
