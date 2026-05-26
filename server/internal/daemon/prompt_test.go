package daemon

import (
	"strings"
	"testing"
)

// TestBuildQuickCreatePromptRules locks in the rules that govern how the
// quick-create agent is allowed to translate raw user input into the issue
// description body. Each substring corresponds to a concrete failure mode
// observed in production output:
//   - meta-instructions ("create an issue", "cc @X") leaking into the body
//   - the Context section being misused as an apology log when no external
//     references were actually fetched
//   - hard-line rules being silently dropped on prompt rewrites
func TestBuildQuickCreatePromptRules(t *testing.T) {
	out := buildQuickCreatePrompt(Task{QuickCreatePrompt: "fix the login button color"})

	mustContain := []string{
		// high-fidelity invariant
		"Faithfully restate what the user wants",
		"Preserve specific names, identifiers, file paths",
		// strip non-spec material: verbal routing wrappers + conversational fillers
		"verbal routing wrappers about creating the issue",
		"pure conversational fillers",
		// cc routing must survive: mention link stays in description so the
		// auto-subscribe path fires (multica issue create has no --subscriber flag)
		"CC exception",
		"auto-subscribes members",
		// context section is conditional and must not be an apology log
		"include ONLY when the input cited external resources",
		"never use it as an apology log",
		// output/reporting must be workspace-prefix agnostic. Workspaces can
		// use custom issue prefixes, so a successful issue creation should
		// not look failed merely because the identifier does not match one
		// fixed prefix.
		"multica issue create --output json",
		"JSON response",
		"identifier",
		"Do not scrape human output",
		"do not assume any workspace issue prefix",
		"Created <identifier-or-id>: <title>",
		// hard rules
		"never invent requirements",
		"never reduce multi-sentence input",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildQuickCreatePrompt output missing required rule: %q", s)
		}
	}
}

// TestBuildQuickCreatePromptProjectPinning verifies that when the user
// pins a project in the quick-create modal, the prompt instructs the agent
// to pass `--project <uuid>` exactly. Without this, the agent would re-read
// the workspace default and silently drop the user's selection — the same
// "I have to retype 'in project X' every time" failure mode the modal
// addition was meant to fix.
func TestBuildQuickCreatePromptProjectPinning(t *testing.T) {
	const projectID = "11111111-2222-3333-4444-555555555555"
	out := buildQuickCreatePrompt(Task{
		QuickCreatePrompt: "fix the login button color",
		ProjectID:         projectID,
		ProjectTitle:      "Web App",
	})
	mustContain := []string{
		"--project \"" + projectID + "\"",
		"Web App",
		"modal selection is authoritative",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildQuickCreatePrompt with project missing %q\n--- output ---\n%s", s, out)
		}
	}

	// Without a project, the prompt must keep the legacy "omit" instruction
	// so the agent doesn't accidentally start passing --project on plain
	// quick-create runs.
	plain := buildQuickCreatePrompt(Task{QuickCreatePrompt: "fix the login button color"})
	if !strings.Contains(plain, "**project**: omit") {
		t.Errorf("buildQuickCreatePrompt without project must keep the omit instruction, got:\n%s", plain)
	}
	if strings.Contains(plain, "--project") {
		t.Errorf("buildQuickCreatePrompt without project must NOT mention --project, got:\n%s", plain)
	}
}

func TestBuildPromptIncludesRelevantProjectKnowledge(t *testing.T) {
	out := BuildPrompt(Task{
		IssueID: "issue-123",
		RelevantKnowledge: []RelevantKnowledgeData{
			{
				TargetType: "wiki_page",
				ID:         "wiki-1",
				Slug:       "runtime-routing",
				Kind:       "wiki_page",
				Outcome:    "reviewed",
				Title:      "Runtime routing",
				Summary:    "Runtime task dispatch uses the Project Wiki as the canonical project understanding layer.",
				Confidence: 90,
			},
		},
	}, "codex")

	mustContain := []string{
		"Project Wiki canonical context:",
		"canonical long-term project understanding layer",
		"kind=wiki_page outcome=reviewed confidence=90 source=wiki-1 slug=runtime-routing",
		"Runtime routing",
		"Project Wiki as the canonical project understanding layer",
		"Wiki delta guidance:",
		"Wiki delta: none",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("BuildPrompt output missing relevant knowledge text %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestBuildChatPromptIncludesProjectWikiInstruction(t *testing.T) {
	out := BuildPrompt(Task{
		ChatSessionID: "chat-123",
		ChatMessage:   `Context: Project "Lost Pet" (id: project-123)` + "\n\nUpdate the wiki from the archived source file.",
	}, "codex")

	mustContain := []string{
		`Context: Project "..." (id: <project-id>)`,
		"active project",
		"multica project wiki",
		"canonical long-term understanding layer",
		"digested and structured, not copied verbatim",
		"durable project knowledge",
		`Context: Project "Lost Pet" (id: project-123)`,
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildChatPrompt output missing project wiki instruction %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestBuildVisualNodePromptRequiresNodeCompletion(t *testing.T) {
	out := BuildPrompt(Task{
		VisualTaskType:               "visual_node_generate",
		ProjectID:                    "project-123",
		ProjectTitle:                 "Lost Pet",
		VisualNodeID:                 "node-123",
		VisualNodeTitle:              "Rainy street corner",
		VisualNodeType:               "scene",
		VisualNodeDescription:        "A warm but rainy urban search scene.",
		VisualPrompt:                 "Cozy rain-soaked street corner, readable missing pet poster.",
		VisualReferenceAttachmentIDs: []string{"attachment-123"},
	}, "codex")

	mustContain := []string{
		"There is no issue for this task",
		"Visual node ID: node-123",
		"download with `multica attachment download attachment-123`",
		"Upload the generated image as an attachment",
		"multica visual-node complete <node-id> --project <project-id> --attachment <local-image-path>",
		"multica visual-node complete <node-id> --project <project-id> --error <reason>",
		"Do not create an issue",
	}

	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("BuildPrompt visual node output missing %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestBuildVisualBoardExtractPromptRequiresStrictJSON(t *testing.T) {
	out := BuildPrompt(Task{
		VisualTaskType: "visual_board_extract",
		ProjectID:      "project-123",
		ProjectTitle:   "Lost Pet",
		VisualBoardID:  "board-123",
		VisualWikiPages: []VisualWikiPageData{
			{
				ID:    "wiki-1",
				Slug:  "visual-brief",
				Title: "Visual Brief",
				Body:  "# 角色：Milo\n一只走失宠物。\n# 场景：雨夜街角",
			},
		},
	}, "codex")

	mustContain := []string{
		"There is no issue for this task",
		"Visual board ID: board-123",
		"Allowed node types: character, scene, ui_element, prop, reference, gameplay_note, generated_variant",
		"wiki_page id=wiki-1 slug=visual-brief",
		"Return exactly one JSON object",
		`"nodes"`,
		`"edges"`,
		"no markdown fences",
	}

	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("BuildPrompt visual board extract output missing %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestBuildIssuePlanSpecPromptPreservesUserLanguage(t *testing.T) {
	out := buildIssuePlanSpecPrompt(Task{
		IssuePlanPrompt: "实现一个 Web 版多人贪吃蛇，先生成 spec 给我确认",
	})

	mustContain := []string{
		"Keep JSON property names exactly as requested in English.",
		"same primary language as the user goal",
		"If the user goal is primarily Chinese",
		"summary, goal, criteria, scope, approach, assumptions, and open questions in Chinese",
		"Keep code identifiers, commands, file paths, API names, and proper nouns unchanged.",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildIssuePlanSpecPrompt output missing language rule: %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestBuildIssuePlanSpecPromptIncludesBuiltInPlannerQualityRules(t *testing.T) {
	out := buildIssuePlanSpecPrompt(Task{
		IssuePlanPrompt: "Ship a risky billing workflow behind review gates",
	})

	mustContain := []string{
		"Planning quality rules:",
		"Treat the user goal as the source of truth.",
		"Success criteria must be observable.",
		"Acceptance scenarios should translate the success criteria into concrete given/when/then cases",
		"Verification commands should be exact runnable commands only when known",
		"Design decisions should record why the proposed approach is chosen",
		"Separate assumptions from open questions.",
		"Put reasonable defaults and non-blocking uncertainties in assumptions",
		"Never ask more than 2 questions in one spec.",
		"Keep in_scope as the smallest coherent delivery slice.",
		"whether review-gated-feature-development should be used for high-risk work.",
		"do not invent agents, skills, repos, files, or commands.",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildIssuePlanSpecPrompt output missing planner quality rule: %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestBuildIssuePlanSpecPromptIncludesClarificationContext(t *testing.T) {
	out := buildIssuePlanSpecPrompt(Task{
		IssuePlanPrompt: "Build plan mode as an interactive flow",
		IssuePlanSpec: PlanSpecData{
			Summary:       "Draft a better plan mode.",
			Goal:          "Make spec review interactive.",
			OpenQuestions: []string{"Which interaction model should it use?"},
			AcceptanceScenarios: []PlanAcceptanceScenarioData{
				{Name: "Review spec", Given: "A draft spec exists", When: "The user approves it", Then: "The planner generates items"},
			},
			DesignDecisions:      []string{"Keep review before item generation."},
			VerificationCommands: []string{"go test ./internal/handler -run TestPlan"},
			Clarifications: []PlanClarificationData{
				{Question: "Which interaction model should it use?", Answer: "Question and answer loop like Superpowers."},
			},
		},
	})

	mustContain := []string{
		"Current draft spec and answered clarifications:",
		"Revise the spec using the user's clarification answers.",
		"Remove answered questions from open_questions.",
		"Question and answer loop like Superpowers.",
		"Given: A draft spec exists",
		"Keep review before item generation.",
		"go test ./internal/handler -run TestPlan",
		`"clarifications": [{"question": "Question answered by the user", "answer": "User answer"}]`,
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildIssuePlanSpecPrompt output missing clarification context: %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestBuildIssuePlanItemsPromptPreservesApprovedSpecLanguage(t *testing.T) {
	out := buildIssuePlanPrompt(Task{
		IssuePlanPrompt: "实现一个 Web 版多人贪吃蛇",
		IssuePlanSpec: PlanSpecData{
			Summary: "用现有 Multica 流程拆出多人贪吃蛇 Web 版本的实现任务。",
			Goal:    "生成可执行 issue 列表。",
		},
	})

	mustContain := []string{
		"same primary language as the approved spec and user goal",
		"If the approved spec or user goal is primarily Chinese",
		"write titles, descriptions, criteria, risk notes, confirmation questions, and confirmation reasons in Chinese",
		"Keep JSON property names, code identifiers, commands, file paths, API names, and proper nouns unchanged.",
		"Set iteration_index, iteration_title, and iteration_branch_name on every direct_issue, item, or selected pipeline node.",
		"The server will force branch_name to the iteration_branch_name for that item.",
		"Do not use `agent/<agent-role>/<issue>` style names.",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildIssuePlanPrompt output missing language rule: %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestBuildIssuePlanItemsPromptIncludesBuiltInPlannerQualityRules(t *testing.T) {
	out := buildIssuePlanPrompt(Task{
		IssuePlanPrompt: "Ship a risky billing workflow behind review gates",
		IssuePlanSpec: PlanSpecData{
			Summary: "Build the workflow safely.",
			Goal:    "Generate executable issue drafts.",
		},
	})

	mustContain := []string{
		"Executable planning rules:",
		"The approved spec is binding.",
		"Every selected item must be independently assignable to one agent",
		"Split by deliverable and dependency boundary, not by job title.",
		"No hidden work:",
		"Use review-gated-feature-development for high-risk feature work",
		"Review gates must depend on the implementation or repair work they review.",
		"Create explicit dependencies for true blocking order",
		"Leave independent work dependency-free so it can run in parallel.",
		"Recommend agents only from Available agents",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("buildIssuePlanPrompt output missing planner quality rule: %q\n--- output ---\n%s", s, out)
		}
	}
}
