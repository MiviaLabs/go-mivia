package httpapi

import (
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
)

func TestSymbolAssemblyKeyUsesNearestAsmdefDirectory(t *testing.T) {
	assemblyByDir := unityAssemblyByDir([]projectingestion.SymbolMetadata{
		{
			Kind:         string(projectingestion.SymbolKindAssembly),
			Name:         "Game.Runtime",
			RelativePath: "Assets/Scripts/Game.asmdef",
		},
		{
			Kind:         string(projectingestion.SymbolKindAssembly),
			Name:         "Game.Features.Inventory",
			RelativePath: "Assets/Scripts/Features/Inventory/Inventory.asmdef",
		},
	})

	tests := []struct {
		name         string
		relativePath string
		want         string
	}{
		{
			name:         "nested asmdef wins",
			relativePath: "Assets/Scripts/Features/Inventory/Slot.cs",
			want:         "Game.Features.Inventory",
		},
		{
			name:         "parent asmdef covers descendants",
			relativePath: "Assets/Scripts/Player/Controller.cs",
			want:         "Game.Runtime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := symbolAssemblyKey(projectingestion.SymbolMetadata{
				Extension:    ".cs",
				RelativePath: tt.relativePath,
			}, assemblyByDir)
			if got != tt.want {
				t.Fatalf("symbolAssemblyKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSymbolAssemblyKeyUsesUnityPredefinedBuckets(t *testing.T) {
	tests := []struct {
		relativePath string
		want         string
	}{
		{relativePath: "Assets/Scripts/Player.cs", want: "Assembly-CSharp"},
		{relativePath: "Assets/Editor/BuildTools.cs", want: "Assembly-CSharp-Editor"},
		{relativePath: "Assets/Plugins/Vendor.cs", want: "Assembly-CSharp-firstpass"},
		{relativePath: "Assets/Plugins/Editor/VendorEditor.cs", want: "Assembly-CSharp-Editor-firstpass"},
		{relativePath: "Assets/Standard Assets/Characters/Controller.cs", want: "Assembly-CSharp-firstpass"},
		{relativePath: "Assets/Standard Assets/Editor/CharacterEditor.cs", want: "Assembly-CSharp-Editor-firstpass"},
		{relativePath: "Assets/Pro Standard Assets/Editor/LegacyEditor.cs", want: "Assembly-CSharp-Editor-firstpass"},
		{relativePath: "assets/plugins/editor/CaseInsensitive.cs", want: "Assembly-CSharp-Editor-firstpass"},
	}

	for _, tt := range tests {
		t.Run(tt.relativePath, func(t *testing.T) {
			got := symbolAssemblyKey(projectingestion.SymbolMetadata{
				Extension:    ".cs",
				RelativePath: tt.relativePath,
			}, nil)
			if got != tt.want {
				t.Fatalf("symbolAssemblyKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSymbolAssemblyKeyIgnoresNonUnityAndNonCSharpSymbols(t *testing.T) {
	tests := []projectingestion.SymbolMetadata{
		{Extension: ".cs", RelativePath: "Packages/com.example/Runtime/Thing.cs"},
		{Extension: ".go", RelativePath: "Assets/Scripts/Thing.go"},
		{Extension: ".asmdef", RelativePath: "Assets/Scripts/Game.asmdef"},
	}

	for _, symbol := range tests {
		if got := symbolAssemblyKey(symbol, nil); got != "" {
			t.Fatalf("symbolAssemblyKey(%+v) = %q, want empty", symbol, got)
		}
	}
}
