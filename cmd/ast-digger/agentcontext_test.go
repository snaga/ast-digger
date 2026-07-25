package main

import (
	"encoding/json"
	"testing"

	"github.com/spf13/pflag"

	"github.com/snaga/ast-digger/internal/apperr"
)

// agentContextPayload mirrors AgentContext's JSON shape for decoding
// `agent-context`'s stdout in tests, independently of the AgentContext type
// itself, so a bug that (for instance) accidentally changed a json tag would
// still be caught by these consistency checks rather than trivially agreeing
// with whatever AgentContext currently marshals to.
type agentContextPayload struct {
	SchemaVersion       string                           `json:"schema_version"`
	CLIName             string                           `json:"cli_name"`
	SupportedLanguages  []string                         `json:"supported_languages"`
	SupportedExtensions []string                         `json:"supported_extensions"`
	Commands            map[string]agentContextCmdSchema `json:"commands"`
}

type agentContextCmdSchema struct {
	Description string                  `json:"description"`
	Arguments   []agentContextArgSchema `json:"arguments"`
	Options     []agentContextOptSchema `json:"options"`
}

type agentContextArgSchema struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  string `json:"default"`
}

type agentContextOptSchema struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

// runAgentContext runs `agent-context --json`, decoding stdout into
// agentContextPayload, and fails the test on any error along the way.
func runAgentContext(t *testing.T) agentContextPayload {
	t.Helper()

	res := runCLI(t, "agent-context")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload agentContextPayload
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	return payload
}

// optionNames returns the set of option names (e.g. "--json") described in
// schema.Options.
func optionNames(schema agentContextCmdSchema) map[string]agentContextOptSchema {
	out := make(map[string]agentContextOptSchema, len(schema.Options))
	for _, opt := range schema.Options {
		out[opt.Name] = opt
	}
	return out
}

// argumentNames returns the set of argument names described in
// schema.Arguments.
func argumentNames(schema agentContextCmdSchema) map[string]agentContextArgSchema {
	out := make(map[string]agentContextArgSchema, len(schema.Arguments))
	for _, arg := range schema.Arguments {
		out[arg.Name] = arg
	}
	return out
}

// TestAgentContextCmd_TopLevelFields checks schema_version/cli_name.
func TestAgentContextCmd_TopLevelFields(t *testing.T) {
	payload := runAgentContext(t)

	if payload.SchemaVersion == "" {
		t.Errorf("payload.SchemaVersion is empty, want a non-empty schema version")
	}
	if payload.CLIName != "ast-digger" {
		t.Errorf("payload.CLIName = %q, want %q", payload.CLIName, "ast-digger")
	}
}

// TestAgentContextCmd_SupportedLanguagesAndExtensions checks supported_languages/supported_extensions.
func TestAgentContextCmd_SupportedLanguagesAndExtensions(t *testing.T) {
	payload := runAgentContext(t)

	wantLangs := []string{"python", "typescript", "golang", "java", "javascript", "rust"}
	if len(payload.SupportedLanguages) != len(wantLangs) {
		t.Fatalf("len(SupportedLanguages) = %d, want %d", len(payload.SupportedLanguages), len(wantLangs))
	}
	for i, lang := range wantLangs {
		if payload.SupportedLanguages[i] != lang {
			t.Errorf("SupportedLanguages[%d] = %q, want %q", i, payload.SupportedLanguages[i], lang)
		}
	}

	wantExts := []string{".py", ".ts", ".tsx", ".go", ".java", ".js", ".jsx", ".rs"}
	if len(payload.SupportedExtensions) != len(wantExts) {
		t.Fatalf("len(SupportedExtensions) = %d, want %d", len(payload.SupportedExtensions), len(wantExts))
	}
	for i, ext := range wantExts {
		if payload.SupportedExtensions[i] != ext {
			t.Errorf("SupportedExtensions[%d] = %q, want %q", i, payload.SupportedExtensions[i], ext)
		}
	}
}

// TestAgentContextCmd_MatchesLiveCommandTree is the integrity test called
// for by task 13: it independently walks newRootCmd()'s registered
// subcommands and their actual pflag.FlagSet definitions (the same
// *cobra.Command tree agent-context itself derives its schema from) and
// asserts every command and every one of its flags shows up, unabridged, in
// `agent-context`'s JSON output. Since both this test and buildAgentContext
// read from the same live command tree, a hand-maintained schema drifting
// away from the real commands/flags (the failure mode requirement H-1
// documented against the Python implementation) is structurally impossible
// to reproduce here -- the only way this test could pass despite a real gap
// is if buildAgentContext itself silently dropped a command/flag, which is
// exactly the regression this test is designed to catch.
func TestAgentContextCmd_MatchesLiveCommandTree(t *testing.T) {
	payload := runAgentContext(t)

	rootCmd := newRootCmd()
	liveCommands := rootCmd.Commands()
	if len(liveCommands) == 0 {
		t.Fatal("newRootCmd() registered zero subcommands; nothing to compare against")
	}

	for _, cmd := range liveCommands {
		name := cmd.Name()

		schema, ok := payload.Commands[name]
		if !ok {
			t.Errorf("agent-context is missing command %q entirely", name)
			continue
		}

		if schema.Description != cmd.Short {
			t.Errorf("command %q: description = %q, want %q (cmd.Short)", name, schema.Description, cmd.Short)
		}

		gotOptions := optionNames(schema)

		var missingFlags []string
		checkFlag := func(f *pflag.Flag) {
			flagName := "--" + f.Name
			opt, present := gotOptions[flagName]
			if !present {
				missingFlags = append(missingFlags, flagName)
				return
			}
			if opt.Description != f.Usage {
				t.Errorf("command %q option %q: description = %q, want %q", name, flagName, opt.Description, f.Usage)
			}
			if opt.Default != f.DefValue {
				t.Errorf("command %q option %q: default = %q, want %q", name, flagName, opt.Default, f.DefValue)
			}
		}

		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) { checkFlag(f) })
		cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) { checkFlag(f) })

		if len(missingFlags) > 0 {
			t.Errorf("command %q is missing options in agent-context output: %v (got: %v)", name, missingFlags, schema.Options)
		}

		// The reverse direction: every option agent-context reports for this
		// command must correspond to a real flag on the live command (no
		// phantom/stale options).
		liveFlagNames := make(map[string]bool)
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) { liveFlagNames["--"+f.Name] = true })
		cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) { liveFlagNames["--"+f.Name] = true })
		for _, opt := range schema.Options {
			if !liveFlagNames[opt.Name] {
				t.Errorf("command %q: agent-context lists option %q which has no corresponding live flag", name, opt.Name)
			}
		}
	}

	// The reverse direction at the command level: agent-context must not
	// describe a command that doesn't actually exist on the live root
	// command (agent-context describing itself is expected and fine).
	liveNames := make(map[string]bool, len(liveCommands))
	for _, cmd := range liveCommands {
		liveNames[cmd.Name()] = true
	}
	for name := range payload.Commands {
		if !liveNames[name] {
			t.Errorf("agent-context describes command %q, which is not registered on the live root command", name)
		}
	}
}

// TestAgentContextCmd_JSONFlagNeverMissing is a focused regression test for
// requirement H-1's specific historical gap: the Python implementation's
// hand-written get_agent_context dict omitted --json from some commands'
// schemas. It asserts --json/-j is present (and typed "boolean") in every
// single registered command's Options, including agent-context's own.
func TestAgentContextCmd_JSONFlagNeverMissing(t *testing.T) {
	payload := runAgentContext(t)

	rootCmd := newRootCmd()
	for _, cmd := range rootCmd.Commands() {
		name := cmd.Name()
		schema, ok := payload.Commands[name]
		if !ok {
			t.Errorf("agent-context is missing command %q", name)
			continue
		}

		opts := optionNames(schema)
		jsonOpt, present := opts["--json"]
		if !present {
			t.Errorf("command %q: agent-context options are missing --json: %v", name, schema.Options)
			continue
		}
		if jsonOpt.Type != "boolean" {
			t.Errorf("command %q: --json type = %q, want %q", name, jsonOpt.Type, "boolean")
		}
	}
}

// TestAgentContextCmd_OutlineDetails pins down the exact shape requested for
// the `outline` command: a required file_path argument, and both --json and
// --no-truncate present among its options.
func TestAgentContextCmd_OutlineDetails(t *testing.T) {
	payload := runAgentContext(t)

	schema, ok := payload.Commands["outline"]
	if !ok {
		t.Fatalf("agent-context is missing the %q command", "outline")
	}

	args := argumentNames(schema)
	filePathArg, ok := args["file_path"]
	if !ok {
		t.Fatalf("outline's arguments are missing %q: %v", "file_path", schema.Arguments)
	}
	if !filePathArg.Required {
		t.Errorf("outline's %q argument: Required = false, want true", "file_path")
	}
	if filePathArg.Type != "string" {
		t.Errorf("outline's %q argument: Type = %q, want %q", "file_path", filePathArg.Type, "string")
	}

	opts := optionNames(schema)
	if _, ok := opts["--json"]; !ok {
		t.Errorf("outline's options are missing --json: %v", schema.Options)
	}
	if _, ok := opts["--no-truncate"]; !ok {
		t.Errorf("outline's options are missing --no-truncate: %v", schema.Options)
	}
}

// TestAgentContextCmd_DirectoryOptionalArgHasDefault checks the optional
// [dir_path] argument on `directory`: not required, and annotated with its
// known "." default.
func TestAgentContextCmd_DirectoryOptionalArgHasDefault(t *testing.T) {
	payload := runAgentContext(t)

	schema, ok := payload.Commands["directory"]
	if !ok {
		t.Fatalf("agent-context is missing the %q command", "directory")
	}

	args := argumentNames(schema)
	dirPathArg, ok := args["dir_path"]
	if !ok {
		t.Fatalf("directory's arguments are missing %q: %v", "dir_path", schema.Arguments)
	}
	if dirPathArg.Required {
		t.Errorf("directory's %q argument: Required = true, want false", "dir_path")
	}
	if dirPathArg.Default != "." {
		t.Errorf("directory's %q argument: Default = %q, want %q", "dir_path", dirPathArg.Default, ".")
	}
}

// TestAgentContextCmd_SymbolTwoRequiredArgs checks that `symbol`'s two
// required positional arguments are both present, in order.
func TestAgentContextCmd_SymbolTwoRequiredArgs(t *testing.T) {
	payload := runAgentContext(t)

	schema, ok := payload.Commands["symbol"]
	if !ok {
		t.Fatalf("agent-context is missing the %q command", "symbol")
	}

	if len(schema.Arguments) != 2 {
		t.Fatalf("symbol's Arguments = %v, want exactly 2 entries", schema.Arguments)
	}
	if schema.Arguments[0].Name != "file_path" || !schema.Arguments[0].Required {
		t.Errorf("symbol's Arguments[0] = %+v, want {Name: file_path, Required: true}", schema.Arguments[0])
	}
	if schema.Arguments[1].Name != "symbol_path" || !schema.Arguments[1].Required {
		t.Errorf("symbol's Arguments[1] = %+v, want {Name: symbol_path, Required: true}", schema.Arguments[1])
	}
}

// TestAgentContextCmd_SelfDescribed checks that agent-context describes
// itself: no required arguments, and --json present among its options
// (inherited from the root command's persistent flag, even though the
// command's own RunE ignores it and always emits JSON).
func TestAgentContextCmd_SelfDescribed(t *testing.T) {
	payload := runAgentContext(t)

	schema, ok := payload.Commands["agent-context"]
	if !ok {
		t.Fatalf("agent-context does not describe itself in its own output")
	}
	if len(schema.Arguments) != 0 {
		t.Errorf("agent-context's own Arguments = %v, want empty", schema.Arguments)
	}
	if _, ok := optionNames(schema)["--json"]; !ok {
		t.Errorf("agent-context's own options are missing --json: %v", schema.Options)
	}
}
