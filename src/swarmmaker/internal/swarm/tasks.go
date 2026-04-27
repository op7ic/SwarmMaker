// tasks.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Task specification builder for Stage 1 generation.
// Creates the 9 task specifications that produce the .tasks/ ledger:
// context.md, tasks.md, prompts/product.md, prompts/technical.md,
// prompts/tools.md, prompts/deployment.md, todo.md, skills.md, and
// agents.md. Each task gets a compiled prompt from the prompt pack.


package swarm

import (
	"github.com/op7ic/swarmmaker/prompts"
)

// BuildTasks creates the stage-1 .tasks-generation task list. Each task
// produces one evidence-backed ledger artifact that later feeds the
// model-specific renderer.
// sourceHints is an ADR-generated preamble based on source complexity analysis
// that gets prepended to each prompt. Pass "" for no hints.
func BuildTasks(ir prompts.PromptIR, sourceHints string) ([]Task, error) {
	pack, err := prompts.DefaultPack()
	if err != nil {
		return nil, err
	}
	return BuildTasksWithPack(ir, sourceHints, pack)
}

// taskSpec describes a single ledger file to generate.
type taskSpec struct {
	name       string
	outputFile string
	kind       prompts.DraftKind
	minLen     int
}

// phaseASpecs returns foundational tasks (context + tasks) that run first.
func phaseASpecs() []taskSpec {
	return []taskSpec{
		{"context", ".tasks/context.md", prompts.DraftContext, 300},
		{"tasks", ".tasks/tasks.md", prompts.DraftTasks, 300},
	}
}

// phaseBSpecs returns dependent tasks that run after phase A completes.
func phaseBSpecs() []taskSpec {
	return []taskSpec{
		{"prompt-product", ".tasks/prompts/product.md", prompts.DraftPromptProduct, 250},
		{"prompt-technical", ".tasks/prompts/technical.md", prompts.DraftPromptTechnical, 250},
		{"prompt-tools", ".tasks/prompts/tools.md", prompts.DraftPromptTools, 220},
		{"prompt-deployment", ".tasks/prompts/deployment.md", prompts.DraftPromptDeployment, 220},
		{"todo", ".tasks/todo.md", prompts.DraftTodo, 250},
		{"skills", ".tasks/skills.md", prompts.DraftSkills, 250},
		{"agents", ".tasks/agents.md", prompts.DraftAgents, 250},
	}
}

// BuildPhaseATasks returns only the foundational tasks (context.md, tasks.md).
func BuildPhaseATasks(ir prompts.PromptIR, sourceHints string, pack prompts.Pack) ([]Task, error) {
	return buildTasksFromSpecs(phaseASpecs(), ir, sourceHints, pack, "")
}

// BuildPhaseBTasks returns the dependent tasks with an optional ledger context
// prefix injected into each prompt.
func BuildPhaseBTasks(ir prompts.PromptIR, sourceHints string, pack prompts.Pack, ledgerContext string) ([]Task, error) {
	return buildTasksFromSpecs(phaseBSpecs(), ir, sourceHints, pack, ledgerContext)
}

func buildTasksFromSpecs(specs []taskSpec, ir prompts.PromptIR, sourceHints string, pack prompts.Pack, ledgerContext string) ([]Task, error) {
	tasks := make([]Task, 0, len(specs))
	for _, spec := range specs {
		prompt, err := prompts.CompileDraftPromptWithPack(spec.kind, ir, pack)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, Task{
			Name:       spec.name,
			OutputFile: spec.outputFile,
			Prompt:     ledgerContext + sourceHints + prompt,
			MinLen:     spec.minLen,
		})
	}
	return tasks, nil
}

// BuildTasksWithPack creates all 9 tasks in a single batch (no two-phase split).
// Retained for backward compatibility and tests that expect a flat task list.
func BuildTasksWithPack(ir prompts.PromptIR, sourceHints string, pack prompts.Pack) ([]Task, error) {
	allSpecs := append(phaseASpecs(), phaseBSpecs()...)
	return buildTasksFromSpecs(allSpecs, ir, sourceHints, pack, "")
}
