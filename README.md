<div align="center">
  <img src="./public/favicon.svg" alt="QuietForge Q logo" width="104" />
  <h1>QuietForge</h1>
  <p><strong>A blazing-fast AI coding agent.</strong></p>
  <p>
    <em>Inspect code. Patch safely. Run commands. Review diffs. Remember architecture.</em>
  </p>
</div>

## 🧠 Core Philosophy: "Thin & Autonomous"

Unlike many agentic frameworks that hardcode rigid state machines and force LLMs down narrow, pre-defined rails, QuietForge believes in **radical LLM autonomy**. 
The backend is intentionally kept *thin*. Instead of micro-managing the AI, QuietForge simply hands the LLM a massive, unrestricted toolkit (shell access, AST parsers, SQLite databases, file system APIs) and limitless memory (artifacts, swarms) and says: *"Here are your tools. Figure it out."*

## 🚀 Key Features

* **Artifact-Based Memory:** QuietForge actively refuses to "memorize" complex architectures or long checklists in its active context window. Instead, it relies on an externalized file system brain (e.g., writing `implementation_plan.md` to disk) to track its thoughts and designs, preventing hallucination bugs and context fatigue.
* **Infinite Short-Term Memory (Anchored Compaction):** QuietForge natively solves context-window overflow. When a session grows too large, it triggers a background LLM process to compress old history into an ultra-dense, continuously updating "Anchored Summary" payload. It intelligently injects this summary at safe turn-boundaries, guaranteeing zero OpenAI sequence schema violations or dangling tool calls.
* **Episodic Long-Term Memory (Active RAG):** QuietForge automatically maintains a local SQLite vector database of your workspace. But beyond just indexing your code AST and `.agent/` documentation, it natively indexes *its own experiences*. After every task, it extracts a JSON episode (Goals, Commands, Errors, Solutions) and embeds it. The agent uses a `semantic_search` tool to actively query this memory, allowing it to autonomously remember how it fixed a specific bug for you weeks ago without passively polluting its context window.
* **Native Swarms:** When tackling massive codebases, QuietForge can use the `invoke_subagent` tool to seamlessly spin up multiple parallel workers. These sub-agents run asynchronously in completely isolated Goroutines and database sessions, allowing them to independently read, plan, and report back without freezing your main UI.
* **Native Git Integration:** QuietForge inherently utilizes Git under the hood. It tracks file diffs, takes pre-execution snapshots, and leverages Git worktrees for isolated agent sandboxing. It features a built-in `revert_workspace` tool for the agent to autonomously reset code, and allows the user to instantly revert specific file changes—or roll back the *entire conversation's* codebase changes—directly from the chat UI.
* **Stateful Task Management:** Utilizing the built-in `todowrite` SQLite database tool, QuietForge autonomously converts markdown plans into dynamic, trackable checklists so it always knows exactly what step it is on.
* **Multi-Agent Routing Profiles:** QuietForge features distinct "brains" (Plan, Build, General, Explore) each equipped with specific tools and permissions, ensuring the right agent tackles the right phase of your project.
* **Automatic Diagnostic Tracking:** QuietForge intercepts terminal outputs and compilation/test failures, extracts syntax or reference errors, and caches them in a structured diagnostics database to help the agent auto-correct bugs.

## 🌟 What Makes It Different?

While there are many AI coding assistants out there, QuietForge stands apart in a few key ways:
* **Blazing Fast Backend:** Written in pure Go with a lightweight Preact frontend, completely side-stepping the heavy overhead of Python backends or bloated Electron wrappers.
* **True Concurrent Swarms:** Unlike other frameworks that just simulate agent loops sequentially, QuietForge leverages **Go Goroutines**. Background sub-agents run truly concurrently, each with their own isolated SQLite database, meaning your main UI and thought-process never freeze.
* **AST-Driven Context Skimming:** Instead of blindly passing thousands of lines of raw code to the LLM, QuietForge uses `go-tree-sitter` to parse Abstract Syntax Trees. It can intelligently strip out massive function bodies on the fly, feeding the agent a condensed "skeleton" of the codebase to skim architectures infinitely faster without blowing up the token window.
* **Multi-Level Context Compression & Token Caching:** When context space gets tight, QuietForge automatically caches token counts on messages to boost speeds and triggers a 3-level compression algorithm (truncating large tool outputs down to 8K or 1K characters, or substituting them with semantic database lookups) to prevent context limit errors.
* **Deterministic State Management:** Instead of hoping the LLM "remembers" its checklist across 100 turns, QuietForge mathematically enforces state. Plans are written to physical markdown files on disk. Tasks and Workspace Graphs are tracked via SQLite rows. The agent's brain is externally anchored.
* **Radical Transparency:** You have 100% control. The SQLite sessions database, the prompt text files, the system logs, and the artifacts are all exposed in your workspace. You can edit the agent's core instructions mid-session just by tweaking a `.txt` file.

## 🛠️ Built-In Tools

QuietForge provides its AI agents with a massive arsenal of capabilities:
- **Code Intelligence:** Centralized, concurrent `LspManager` that runs language servers (`gopls`, `typescript-language-server`, `pylsp`, `rust-analyzer`) on demand, synchronizing modifications via JSON-RPC, alongside native `ast_search` for semantic codebase navigation.
- **System Access:** Hardened file access tools (`edit`, `apply_patch` utilizing path-jailing to protect the workspace), precise creation (`write`), full terminal execution (`shell`), and semantic queries (`grep`, `glob`).
- **Web Navigation:** Autonomous web searching (`websearch`) and fetching (`webfetch`) for digging through documentation.
- **Extensibility:** First-class support for MCP (Model Context Protocol) servers, allowing you to plug in any external tools.

## ⚙️ How It Works (The Agent Lifecycle)

1. **Planning Mode (The Traffic Cop):** When you drop a massive feature request into QuietForge, the agent will refuse to write code immediately. It will switch into a research phase, explore your codebase, and use the `write_artifact` tool to author a physical `implementation_plan.md`.
2. **User Approval:** QuietForge pauses and presents the proposed markdown plan for your review.
3. **Execution & Swarming:** Once approved, the Build agent wakes up. If the task is massive, it may delegate isolated chunks of work to background Swarm sub-agents. It uses the `todowrite` tool to track its progress step-by-step.
4. **Validation:** The agent uses its shell tools to compile, run tests, and verify its logic before presenting you with a final walkthrough of the changes.

## 🧠 Customizing Agent Behavior

QuietForge supports powerful local customization by reading from a `.agents` directory placed directly inside your project's workspace:
* **`AGENTS.md`:** A workspace-specific rules file where you can define strict project guidelines, styling standards, or architectural constraints. The agent will automatically ingest and adhere to these rules on every interaction.
* **`skills/` Directory:** You can teach QuietForge new workflows by dropping markdown-based "Skills" (e.g., custom deployment scripts, testing protocols) into this folder. When the agent recognizes a specialized task, it dynamically loads your custom skill instructions into its context window.

## ⚙️ Project Configuration (.quietforge)

Beyond agent-specific behavior, QuietForge also supports a `.quietforge` directory for core application settings and plugins. Here is what a typical project directory looks like:

```text
your-project/
├── .quietforge/
│   ├── workspaces/
│   └── config.json
└── quietforge.exe
```

* **`config.json`**: Override default LLM models, set API keys, or tweak core engine parameters. **Vision Support:** Set `"disable_vision": true` if your chosen LLM does not support image inputs to prevent API crash loops. **Security features:** Adding a `password` field will lock down the web UI, and providing `ssl_cert` and `ssl_key` paths will automatically serve QuietForge over HTTPS.
* **`tools/` Directory**: Drop custom tools or external tool definitions here. QuietForge will dynamically load them into the agent's tool registry!
* **`workspaces/` Directory**: Used internally by QuietForge to manage isolated Git worktrees and temporary session environments.

## 💡 Inspirations

QuietForge was built on the shoulders of giants:
* <a href="https://opencode.ai" target="_blank">**OpenCode**</a>: Influenced the robust codebase search functionality and precise patch-editing systems.
* <a href="https://antigravity.google" target="_blank">**Antigravity**</a>: Heavily inspired the externalized memory architectures, the strict separation of Planning vs Building, and the asynchronous Native Swarm goroutine design.

## 🔧 Getting Started

### Option 1: Download Pre-compiled Binary (Recommended)
You can download the ready-to-run binary for Windows, macOS, or Linux directly from the [GitHub Releases](https://github.com/anbuhckr/QuietForge/releases) page. No installation required!

### Option 2: Build from Source
*(Assuming you have Go installed)*

```bash
# Clone the repository
git clone https://github.com/anbuhckr/QuietForge.git
cd QuietForge

# Build the engine (CGO is required for tree-sitter AST search)

# For Windows (PowerShell):
$env:CGO_ENABLED=1; go build

# For Linux / macOS (Bash/Zsh):
CGO_ENABLED=1 go build

# Run the server
./quietforge
```

By default, the server spins up a frontend UI at `http://localhost`. Select your Agent Mode, drop in a massive prompt, and watch the forge go to work!
