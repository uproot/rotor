package pack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"rotor/internal/luau/cst"
)

// writeFixtureProject builds a Rojo project exercising the native builder's supported
// surface: nested folders, an init directory, .server/.client scripts, and a .txt
// StringValue. Returns the project file path.
func writeFixtureProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, "src", "util"))
	writeFile(t, filepath.Join(dir, "src", "Alpha.luau"), "return 1\n")
	writeFile(t, filepath.Join(dir, "src", "boot.server.luau"), "print(\"server\")\n")
	writeFile(t, filepath.Join(dir, "src", "ui.client.luau"), "print(\"client\")\n")
	writeFile(t, filepath.Join(dir, "src", "util", "init.luau"), "return { v = require(script.Alpha) }\n")
	writeFile(t, filepath.Join(dir, "src", "util", "Alpha.luau"), "return 2\n")
	writeFile(t, filepath.Join(dir, "src", "util", "note.txt"), "hello\n")
	writeFile(t, filepath.Join(dir, "default.project.json"),
		"{ \"name\": \"demo\", \"tree\": { \"$className\": \"Folder\", \"src\": { \"$path\": \"src\" } } }\n")
	return filepath.Join(dir, "default.project.json")
}

// canonical renders an instance tree as a stable, sibling-order-independent string of
// (class, name, source, value), for structural equality checks.
func canonical(inst *Instance) string {
	var b strings.Builder
	var walk func(n *Instance, depth int)
	walk = func(n *Instance, depth int) {
		fmt.Fprintf(&b, "%s%s:%s src=%q val=%q\n",
			strings.Repeat("  ", depth), n.ClassName, n.Name, n.Source, n.Value)
		kids := append([]*Instance(nil), n.Children...)
		sort.Slice(kids, func(i, j int) bool { return kids[i].Name < kids[j].Name })
		for _, c := range kids {
			walk(c, depth+1)
		}
	}
	walk(inst, 0)
	return b.String()
}

// TestNativeMatchesRojo is the 1:1 gate: for a supported project, the native instance
// tree must be structurally identical to the one `rojo build` produces (same classes,
// names, sources, values, structure). Skips without rojo.
func TestNativeMatchesRojo(t *testing.T) {
	if _, err := exec.LookPath("rojo"); err != nil {
		t.Skip("rojo not on PATH")
	}
	project := writeFixtureProject(t)

	nativeRoot, supported, err := BuildNative(project)
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Fatal("fixture should be natively supported")
	}

	rojoRoots, err := buildTreeViaRojo(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(rojoRoots) != 1 {
		t.Fatalf("rojo produced %d roots", len(rojoRoots))
	}

	if got, want := canonical(nativeRoot), canonical(rojoRoots[0]); got != want {
		t.Fatalf("native tree != rojo tree\n--- native ---\n%s\n--- rojo ---\n%s", got, want)
	}
}

// TestNativeUnsupportedFallsBack verifies the native builder declines a project with a
// non-script file (here a .json module), so Pack would fall back to rojo.
func TestNativeUnsupportedFallsBack(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, "src"))
	writeFile(t, filepath.Join(dir, "src", "config.json"), "{ \"x\": 1 }\n")
	writeFile(t, filepath.Join(dir, "default.project.json"),
		"{ \"name\": \"demo\", \"tree\": { \"$className\": \"Folder\", \"src\": { \"$path\": \"src\" } } }\n")
	_, supported, err := BuildNative(filepath.Join(dir, "default.project.json"))
	if err != nil {
		t.Fatal(err)
	}
	if supported {
		t.Fatal("a .json module should make the project natively unsupported")
	}
}

const sampleRbxmx = `<roblox version="4">
  <Item class="Folder" referent="0">
    <Properties><string name="Name">demo</string></Properties>
    <Item class="ModuleScript" referent="1">
      <Properties>
        <string name="Name">greet</string>
        <string name="Source"><![CDATA[return function(name) return "Hi " .. name end]]></string>
      </Properties>
    </Item>
    <Item class="StringValue" referent="2">
      <Properties>
        <string name="Name">note</string>
        <string name="Value"><![CDATA[hello]]></string>
      </Properties>
    </Item>
  </Item>
</roblox>`

func TestParseRbxmx(t *testing.T) {
	roots, err := ParseRbxmx([]byte(sampleRbxmx))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots = %d", len(roots))
	}
	root := roots[0]
	if root.ClassName != "Folder" || root.Name != "demo" || len(root.Children) != 2 {
		t.Fatalf("root = %+v", root)
	}
	greet := root.Children[0]
	if greet.ClassName != "ModuleScript" || greet.Name != "greet" || !strings.Contains(greet.Source, "Hi ") {
		t.Fatalf("greet = %+v", greet)
	}
	note := root.Children[1]
	if note.ClassName != "StringValue" || note.Value != "hello" {
		t.Fatalf("note = %+v", note)
	}
}

func TestEmitLuauParses(t *testing.T) {
	roots, _ := ParseRbxmx([]byte(sampleRbxmx))
	src, err := EmitLuau(roots, "demo.greet")
	if err != nil {
		t.Fatal(err)
	}
	if _, diags := cst.Parse(src); len(diags) != 0 {
		t.Fatalf("emitted bundle does not parse: %s\n---\n%s", diags[0].Message, src)
	}
	// a bad entry path errors
	if _, err := EmitLuau(roots, "demo.nope"); err == nil {
		t.Fatalf("expected error for missing entry")
	}
}

// TestPackLuauRunsUnderLune is the headline proof: a Rojo project, packed to a single
// self-reconstructing Luau script, runs and resolves instance-path requires. Skips
// without rojo + lune.
func TestPackLuauRunsUnderLune(t *testing.T) {
	rojo, err := exec.LookPath("rojo")
	if err != nil {
		t.Skip("rojo not on PATH")
	}
	lune, err := exec.LookPath("lune")
	if err != nil {
		t.Skip("lune not on PATH")
	}
	_ = rojo

	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, "src"))
	writeFile(t, filepath.Join(dir, "src", "greet.luau"), "return function(name) return \"Hi \" .. name end\n")
	writeFile(t, filepath.Join(dir, "src", "main.luau"), "return require(script.Parent.greet)(\"world\")\n")
	writeFile(t, filepath.Join(dir, "default.project.json"),
		"{ \"name\": \"demo\", \"tree\": { \"$className\": \"Folder\", \"src\": { \"$path\": \"src\" } } }\n")

	bundle, err := Pack(Options{Project: dir, Format: FormatLuau, Entry: "demo.src.main"})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	writeFile(t, filepath.Join(dir, "bundle.luau"), string(bundle))

	// the bundle returns require(main); print it so we can observe the value.
	// lune resolves "./bundle" relative to the requiring file (run.luau in dir).
	writeFile(t, filepath.Join(dir, "run.luau"), "print(require(\"./bundle\"))\n")
	cmd := exec.Command(lune, "run", "run.luau")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lune run failed: %v\n%s\n--- bundle ---\n%s", err, out, bundle)
	}
	if !strings.Contains(string(out), "Hi world") {
		t.Fatalf("unexpected output: %q", out)
	}
}

// TestPackVirtualGameResolvesUnderLune proves a DataModel-rooted bundle resolves an
// absolute `game:GetService(...)`/`WaitForChild` path against the reconstructed tree, so
// it stays self-contained even with no host game (lune has no `game` global).
func TestPackVirtualGameResolvesUnderLune(t *testing.T) {
	lune, err := exec.LookPath("lune")
	if err != nil {
		t.Skip("lune not on PATH")
	}
	greet := &Instance{ClassName: "ModuleScript", Name: "greet", Source: `return "hi world"`}
	lib := &Instance{ClassName: "Folder", Name: "Lib", Children: []*Instance{greet}}
	rs := &Instance{ClassName: "ReplicatedStorage", Name: "ReplicatedStorage", Children: []*Instance{lib}}
	main := &Instance{ClassName: "ModuleScript", Name: "main",
		Source: `return require(game:GetService("ReplicatedStorage"):WaitForChild("Lib"):WaitForChild("greet"))`}
	root := &Instance{ClassName: "DataModel", Name: "game", Children: []*Instance{rs, main}}

	out, err := EmitLuau([]*Instance{root}, "game.main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "local game = i") {
		t.Fatalf("a DataModel bundle should shadow `game` with the virtual root:\n%s", out)
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bundle.luau"), out)
	writeFile(t, filepath.Join(dir, "run.luau"), "print(require(\"./bundle\"))\n")
	cmd := exec.Command(lune, "run", "run.luau")
	cmd.Dir = dir
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lune run failed: %v\n%s\n--- bundle ---\n%s", err, got, out)
	}
	if !strings.Contains(string(got), "hi world") {
		t.Fatalf("got %q, want \"hi world\"\n--- bundle ---\n%s", got, out)
	}
}

// TestPackStringRequiresResolveUnderLune proves Luau require-by-string paths
// (./x, ../x, nested a/b, @self/x) used by pesde-style packages (e.g. @rbxts/ripple,
// @rbxts/react-charm) resolve against the reconstructed tree, mirroring a package's
// real shape: <scope>/<pkg>/src (init module) with submodules under it.
func TestPackStringRequiresResolveUnderLune(t *testing.T) {
	lune, err := exec.LookPath("lune")
	if err != nil {
		t.Skip("lune not on PATH")
	}
	leaf := &Instance{ClassName: "ModuleScript", Name: "leaf", Source: `return "LEAF"`}
	h := &Instance{ClassName: "ModuleScript", Name: "h", Source: `return require("../leaf") .. "HELP"`}
	util := &Instance{ClassName: "Folder", Name: "util", Children: []*Instance{h}}
	// src is the package's init module (has children), so "./x" is a child and
	// "@self" is src itself.
	src := &Instance{ClassName: "ModuleScript", Name: "src",
		Source:   `return require("./util/h") .. "|" .. require("@self/leaf")`,
		Children: []*Instance{leaf, util}}
	pkg := &Instance{ClassName: "Folder", Name: "ripple", Children: []*Instance{src}}
	scope := &Instance{ClassName: "Folder", Name: "@rbxts", Children: []*Instance{pkg}}
	nm := &Instance{ClassName: "Folder", Name: "node_modules", Children: []*Instance{scope}}
	root := &Instance{ClassName: "Folder", Name: "app", Children: []*Instance{nm}}

	out, err := EmitLuau([]*Instance{root}, "app.node_modules.@rbxts.ripple.src")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bundle.luau"), out)
	writeFile(t, filepath.Join(dir, "run.luau"), "print(require(\"./bundle\"))\n")
	cmd := exec.Command(lune, "run", "run.luau")
	cmd.Dir = dir
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lune run failed: %v\n%s\n--- bundle ---\n%s", err, got, out)
	}
	if !strings.Contains(string(got), "LEAFHELP|LEAF") {
		t.Fatalf("string requires did not resolve: got %q, want LEAFHELP|LEAF\n--- bundle ---\n%s", got, out)
	}
}

// TestPackScriptEntryRunsViaClosure verifies a Script/LocalScript --entry is invoked via
// its closure (scripts are not requirable) rather than passed to rotorRequire.
func TestPackScriptEntryRunsViaClosure(t *testing.T) {
	boot := &Instance{ClassName: "LocalScript", Name: "boot", Source: `print("booted")`}
	root := &Instance{ClassName: "Folder", Name: "app", Children: []*Instance{boot}}

	out, err := EmitLuau([]*Instance{root}, "app.boot")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "return rotorRequire(i") {
		t.Fatalf("a script entry must not be passed to rotorRequire:\n%s", out)
	}
	if !strings.Contains(out, "._impl(i") {
		t.Fatalf("a script entry should run via its _impl closure:\n%s", out)
	}
	if _, diags := cst.Parse(out); len(diags) != 0 {
		t.Fatalf("bundle does not parse: %s\n%s", diags[0].Message, out)
	}

	lune, err := exec.LookPath("lune")
	if err != nil {
		t.Skip("lune not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bundle.luau"), out)
	writeFile(t, filepath.Join(dir, "run.luau"), "require(\"./bundle\")\n")
	cmd := exec.Command(lune, "run", "run.luau")
	cmd.Dir = dir
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lune run failed: %v\n%s", err, got)
	}
	if !strings.Contains(string(got), "booted") {
		t.Fatalf("got %q, want \"booted\"", got)
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
