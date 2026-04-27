// registry.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Output renderer registry.
// Manages the set of registered renderers (one per output format). Handles
// duplicate detection, format/spec consistency validation, and automatic
// canonicalization and validation of rendered manifests.


package output

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnsupportedFormat is returned when a caller requests an unregistered tree.
var ErrUnsupportedFormat = errors.New("unsupported output format")

// Renderer turns shared blueprint data into a format-specific manifest.
type Renderer interface {
	Format() Format
	Spec() TreeSpec
	Render(Blueprint) (Manifest, error)
}

// Registry stores the renderers for supported output trees.
type Registry struct {
	renderers map[Format]Renderer
	specs     map[Format]TreeSpec
}

// NewRegistry builds a registry from the provided renderers.
func NewRegistry(renderers ...Renderer) (*Registry, error) {
	reg := &Registry{
		renderers: make(map[Format]Renderer, len(renderers)),
		specs:     make(map[Format]TreeSpec, len(renderers)),
	}
	if len(renderers) == 0 {
		renderers = defaultRenderers()
	}
	for _, renderer := range renderers {
		if renderer == nil {
			return nil, fmt.Errorf("nil renderer")
		}
		format := renderer.Format()
		if format == "" {
			return nil, fmt.Errorf("renderer with empty format")
		}
		if _, exists := reg.renderers[format]; exists {
			return nil, fmt.Errorf("duplicate renderer for format %q", format)
		}
		spec := renderer.Spec()
		if spec.Format != format {
			return nil, fmt.Errorf("renderer/spec format mismatch for %q", format)
		}
		reg.renderers[format] = renderer
		reg.specs[format] = spec
	}
	return reg, nil
}

// SupportedFormats returns the registered formats in deterministic order.
func (r *Registry) SupportedFormats() []Format {
	formats := make([]Format, 0, len(r.renderers))
	for format := range r.renderers {
		formats = append(formats, format)
	}
	sort.Slice(formats, func(i, j int) bool { return formats[i] < formats[j] })
	return formats
}

// Spec returns the contract for a supported format.
func (r *Registry) Spec(format Format) (TreeSpec, bool) {
	spec, ok := r.specs[format]
	return spec, ok
}

// Render executes a renderer and validates the resulting manifest.
func (r *Registry) Render(format Format, blueprint Blueprint) (Manifest, error) {
	renderer, ok := r.renderers[format]
	if !ok {
		return Manifest{}, fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
	manifest, err := renderer.Render(blueprint)
	if err != nil {
		return Manifest{}, err
	}
	manifest = CanonicalizeManifest(manifest)
	if err := ValidateManifest(r.specs[format], manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func defaultRenderers() []Renderer {
	specs := DefaultSpecs()
	return []Renderer{
		newTreeRenderer(specs[FormatClaude]),
		newTreeRenderer(specs[FormatCodex]),
		newTreeRenderer(specs[FormatGemini]),
	}
}
