// discovery.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// LLM CLI tool discovery.
// Scans the system PATH for known LLM CLI binaries (claude, codex, gemini),
// probes their version and capabilities, and returns a structured inventory.
// Supports platform-aware binary resolution including Windows .exe variants.


package discovery

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Capability describes a role a provider can take in the swarm pipeline.
type Capability string

const (
	CapabilityGenerate     Capability = "generate"
	CapabilityCritique     Capability = "critique"
	CapabilityRenderOutput Capability = "render_output"
	CapabilityBuildTools   Capability = "build_tools"
)

var supportedCapabilities = map[Capability]struct{}{
	CapabilityGenerate:     {},
	CapabilityCritique:     {},
	CapabilityRenderOutput: {},
	CapabilityBuildTools:   {},
}

func (c Capability) String() string {
	return string(c)
}

func isKnownCapability(c Capability) bool {
	_, ok := supportedCapabilities[c]
	return ok
}

// LLMTool represents a discovered LLM CLI tool on the system.
type LLMTool struct {
	Name         string       // e.g. "claude", "codex", "gemini"
	Path         string       // absolute path to binary
	Version      string       // version string if detectable
	Available    bool         // whether the tool was found
	Capabilities []Capability // explicit role metadata for routing
}

// knownTools defines the LLM CLI tools we search for, with their
// version detection commands.
var knownTools = []struct {
	Name         string
	Binaries     []string // possible binary names (platform variants)
	VersionCmd   []string // command to get version
	Capabilities []Capability
}{
	{
		Name:       "claude",
		Binaries:   []string{"claude"},
		VersionCmd: []string{"claude", "--version"},
		Capabilities: []Capability{
			CapabilityGenerate,
			CapabilityCritique,
			CapabilityRenderOutput,
			CapabilityBuildTools,
		},
	},
	{
		Name:       "codex",
		Binaries:   []string{"codex"},
		VersionCmd: []string{"codex", "--version"},
		Capabilities: []Capability{
			CapabilityGenerate,
			CapabilityCritique,
			CapabilityBuildTools,
		},
	},
	{
		Name:       "gemini",
		Binaries:   []string{"gemini"},
		VersionCmd: []string{"gemini", "--version"},
		Capabilities: []Capability{
			CapabilityGenerate,
			CapabilityCritique,
			CapabilityRenderOutput,
		},
	},
	{
		Name:       "ollama",
		Binaries:   []string{"ollama"},
		VersionCmd: []string{"ollama", "--version"},
		Capabilities: []Capability{
			CapabilityGenerate,
			CapabilityCritique,
		},
	},
}

var (
	lookPathFunc      = exec.LookPath
	versionOutputFunc = func(cmd []string) ([]byte, error) {
		if len(cmd) == 0 {
			return nil, errors.New("empty command")
		}
		return exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
	}
)

// FindAllLLMs searches the system PATH for all known LLM CLI tools.
func FindAllLLMs() []LLMTool {
	results := make([]LLMTool, 0, len(knownTools))

	for _, tool := range knownTools {
		found := LLMTool{
			Name:         tool.Name,
			Available:    false,
			Capabilities: append([]Capability(nil), tool.Capabilities...),
		}

		for _, bin := range tool.Binaries {
			binName := bin
			if runtime.GOOS == "windows" {
				// On Windows, also check for .exe and .cmd variants
				variants := []string{bin, bin + ".exe", bin + ".cmd", bin + ".bat"}
				for _, v := range variants {
					if path, err := lookPathFunc(v); err == nil {
						found.Path = path
						found.Available = true
						break
					}
				}
			} else {
				if path, err := lookPathFunc(binName); err == nil {
					found.Path = path
					found.Available = true
				}
			}

			if found.Available {
				break
			}
		}

		// Try to detect version
		if found.Available && len(tool.VersionCmd) > 0 {
			found.Version = detectVersion(tool.VersionCmd)
		}

		results = append(results, found)
	}

	return results
}

// ValidateMetadata verifies the provider metadata is internally consistent.
func (t LLMTool) ValidateMetadata() error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("provider name is required")
	}
	if len(t.Capabilities) == 0 {
		return errors.New("provider capabilities are required")
	}
	for _, capability := range t.Capabilities {
		if !isKnownCapability(capability) {
			return fmt.Errorf("provider %q has unsupported capability %q", t.Name, capability)
		}
	}
	if t.Available && strings.TrimSpace(t.Path) == "" {
		return fmt.Errorf("provider %q is marked available but has no binary path", t.Name)
	}
	return nil
}

// ValidateKnownMetadata verifies the metadata and also requires a known provider name.
func (t LLMTool) ValidateKnownMetadata() error {
	if err := t.ValidateMetadata(); err != nil {
		return err
	}
	if !IsKnownToolName(t.Name) {
		return fmt.Errorf("provider %q is not a known CLI tool", t.Name)
	}
	return nil
}

// Supports reports whether the provider advertises a capability.
func (t LLMTool) Supports(capability Capability) bool {
	for _, existing := range t.Capabilities {
		if existing == capability {
			return true
		}
	}
	return false
}

// IsKnownToolName reports whether the discovery layer recognizes the provider name.
func IsKnownToolName(name string) bool {
	for _, tool := range knownTools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// CapabilitiesForTool returns a copy of the default capability set for a known tool.
func CapabilitiesForTool(name string) []Capability {
	for _, tool := range knownTools {
		if tool.Name == name {
			return append([]Capability(nil), tool.Capabilities...)
		}
	}
	return nil
}

func detectVersion(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}

	out, _ := versionOutputFunc(cmd)
	version := strings.TrimSpace(string(out))
	// Take first line only
	if idx := strings.IndexByte(version, '\n'); idx >= 0 {
		version = version[:idx]
	}

	return version
}
