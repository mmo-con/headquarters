package config

import (
	"maps"
	"slices"
)

// ThemePresets are the built-in color schemes selectable with
// [ui.theme].preset. The ANSI-based presets follow the terminal's palette and
// suit light and dark backgrounds; the others use fixed colors and come in a
// dark and a light variant.
var ThemePresets = map[string]Theme{
	"default": DefaultTheme(),
	"high-contrast": {
		ActiveBorder: "bright-cyan", InactiveBorder: "default", Title: "bright-cyan",
		SelectedBackground: "blue", SelectedForeground: "bright-white", SelectedAccent: "bright-yellow",
		HeaderBackground: "white", HeaderForeground: "black",
		Key: "bright-cyan", Branch: "bright-cyan", Clean: "default", Dirty: "bright-yellow",
		Ahead: "bright-green", Behind: "bright-magenta", Success: "bright-green",
		Error: "bright-red", Warning: "bright-yellow", Dim: "default",
	},
	"low-color": {
		ActiveBorder: "green", InactiveBorder: "default", Title: "green",
		SelectedBackground: "blue", SelectedForeground: "white", SelectedAccent: "cyan",
		HeaderBackground: "cyan", HeaderForeground: "black",
		Key: "cyan", Branch: "cyan", Clean: "default", Dirty: "yellow",
		Ahead: "green", Behind: "magenta", Success: "green",
		Error: "red", Warning: "yellow", Dim: "default",
	},
	"monochrome": {
		ActiveBorder: "default", InactiveBorder: "gray", Title: "default",
		SelectedBackground: "white", SelectedForeground: "black", SelectedAccent: "black",
		HeaderBackground: "gray", HeaderForeground: "bright-white",
		Key: "default", Branch: "default", Clean: "gray", Dirty: "default",
		Ahead: "default", Behind: "default", Success: "default",
		Error: "default", Warning: "default", Dim: "gray",
	},
	"catppuccin-mocha": {
		ActiveBorder: "#89b4fa", InactiveBorder: "#6c7086", Title: "#cba6f7",
		SelectedBackground: "#45475a", SelectedForeground: "#cdd6f4", SelectedAccent: "#cba6f7",
		HeaderBackground: "#cba6f7", HeaderForeground: "#11111b",
		Key: "#89dceb", Branch: "#94e2d5", Clean: "#7f849c", Dirty: "#f9e2af",
		Ahead: "#a6e3a1", Behind: "#f5c2e7", Success: "#a6e3a1",
		Error: "#f38ba8", Warning: "#fab387", Dim: "#7f849c",
	},
	"catppuccin-latte": {
		ActiveBorder: "#1e66f5", InactiveBorder: "#9ca0b0", Title: "#8839ef",
		SelectedBackground: "#ccd0da", SelectedForeground: "#4c4f69", SelectedAccent: "#8839ef",
		HeaderBackground: "#8839ef", HeaderForeground: "#eff1f5",
		Key: "#04a5e5", Branch: "#179299", Clean: "#8c8fa1", Dirty: "#df8e1d",
		Ahead: "#40a02b", Behind: "#ea76cb", Success: "#40a02b",
		Error: "#d20f39", Warning: "#fe640b", Dim: "#8c8fa1",
	},
	"dracula": {
		ActiveBorder: "#bd93f9", InactiveBorder: "#6272a4", Title: "#ff79c6",
		SelectedBackground: "#44475a", SelectedForeground: "#f8f8f2", SelectedAccent: "#bd93f9",
		HeaderBackground: "#bd93f9", HeaderForeground: "#282a36",
		Key: "#8be9fd", Branch: "#8be9fd", Clean: "#6272a4", Dirty: "#f1fa8c",
		Ahead: "#50fa7b", Behind: "#ff79c6", Success: "#50fa7b",
		Error: "#ff5555", Warning: "#ffb86c", Dim: "#6272a4",
	},
	"onedark": {
		ActiveBorder: "#61afef", InactiveBorder: "#5c6370", Title: "#c678dd",
		SelectedBackground: "#3e4451", SelectedForeground: "#dcdfe4", SelectedAccent: "#c678dd",
		HeaderBackground: "#61afef", HeaderForeground: "#282c34",
		Key: "#56b6c2", Branch: "#61afef", Clean: "#7f848e", Dirty: "#e5c07b",
		Ahead: "#98c379", Behind: "#c678dd", Success: "#98c379",
		Error: "#e06c75", Warning: "#d19a66", Dim: "#7f848e",
	},
	"monokai": {
		ActiveBorder: "#a6e22e", InactiveBorder: "#75715e", Title: "#a6e22e",
		SelectedBackground: "#49483e", SelectedForeground: "#f8f8f2", SelectedAccent: "#ae81ff",
		HeaderBackground: "#66d9ef", HeaderForeground: "#272822",
		Key: "#66d9ef", Branch: "#66d9ef", Clean: "#75715e", Dirty: "#e6db74",
		Ahead: "#a6e22e", Behind: "#ae81ff", Success: "#a6e22e",
		Error: "#f92672", Warning: "#fd971f", Dim: "#75715e",
	},
	"gruvbox-dark": {
		ActiveBorder: "#83a598", InactiveBorder: "#665c54", Title: "#fe8019",
		SelectedBackground: "#504945", SelectedForeground: "#ebdbb2", SelectedAccent: "#fabd2f",
		HeaderBackground: "#458588", HeaderForeground: "#fbf1c7",
		Key: "#8ec07c", Branch: "#83a598", Clean: "#928374", Dirty: "#fabd2f",
		Ahead: "#b8bb26", Behind: "#d3869b", Success: "#b8bb26",
		Error: "#fb4934", Warning: "#fe8019", Dim: "#928374",
	},
	"gruvbox-light": {
		ActiveBorder: "#076678", InactiveBorder: "#a89984", Title: "#af3a03",
		SelectedBackground: "#d5c4a1", SelectedForeground: "#3c3836", SelectedAccent: "#b57614",
		HeaderBackground: "#076678", HeaderForeground: "#fbf1c7",
		Key: "#427b58", Branch: "#076678", Clean: "#7c6f64", Dirty: "#b57614",
		Ahead: "#79740e", Behind: "#8f3f71", Success: "#79740e",
		Error: "#9d0006", Warning: "#af3a03", Dim: "#7c6f64",
	},
}

// ThemePresetNames returns the preset names in alphabetical order.
func ThemePresetNames() []string {
	return slices.Sorted(maps.Keys(ThemePresets))
}
