# Worker context at start: prior art

V = stated in docs, source code, or an engineering post. U = inferred.

## 1. Anthropic research system
- The lead gives each subagent "an objective, an output format, guidance on the tools and sources to use, and clear task boundaries". V [1]
- Short tasks ("research the semiconductor shortage") caused duplicate work between subagents. V [1]
- Token usage explains 80% of the performance variance on BrowseComp. V [1]

## 2. Claude Code
- A subagent starts with its own system prompt, the delegation prompt, the CLAUDE.md hierarchy, a git status snapshot, and preloaded skills. It does not get the parent conversation. V [2]
- A "fork" subagent is the exception: it inherits the parent conversation. V [2]
- An agent-team teammate loads CLAUDE.md, MCP servers, skills, and the spawn prompt. "The lead's conversation history does not carry over." V [3]
- Projects: every cloud thread starts with project instructions (the "brief", maximum 16,000 characters), repository CLAUDE.md files, and skills. It reads the memory index `MEMORY.md` at start and opens other memory files on demand. V [4]

## 3. Cognition / Devin
- June 2025: "Share context, and share full agent traces"; "Actions carry implicit decisions". V [5]
- A managed Devin gets "a clean slate, a narrow focus". The parent can send context mid-task. V [6]
- 2026: the coder agent and the review agent work best when they "do not share any context beforehand". The reviewer sees only the diff. V [7]
- Managers "default to being overly prescriptive", which fails when the manager lacks codebase context. "Agents assume they share state with their children when they don't." V [7]

## 4. Other systems
- Codex: a custom agent defines `developer_instructions`; the child inherits model, sandbox, and MCP servers. V [8]
- Codex: `fork_turns` defaults to "all" (full parent history); the issue quotes the source line. V [9]
- Codex: with `fork_context: true`, subagents "continue the parent thread's active role/work" instead of the delegated prompt (user report). V [10]
- Cursor: "Subagents start with a clean context. The parent agent includes relevant information in the prompt." V [11]
- OpenHands: each sub-agent has its own conversation, and shares the workspace with the parent. V [12]
- Copilot coding agent: gets the issue title, description, current comments, and extra instructions. It ignores later issue comments. V [13]
- Jules: task prompt, repository, branch, and `AGENTS.md`. V [14]
- Factory Missions: workers get `AGENTS.md`, skills, MCP, and custom droids. V [15] Workers get one plan item, not the full plan. U
- Amp: subagents "start with the instructions and context the main agent gives them rather than the full conversation". V [16] An orb thread has its own context window. V [17]

## 5. Evidence on context length
- Recall decreases as token count increases; aim for "the smallest possible set of high-signal tokens". V [18]
- Chroma, 18 models: performance becomes less reliable as input length increases. A focused prompt (about 300 tokens) scored higher than the full prompt (about 113k tokens) for all models. V [19]
- Models use information at the start or end of a context better than information in the middle. V [20]
- AGENTS.md files do not generally improve task success and increase cost by more than 20%. V [21]

## Patterns
- Most workers start clean: role prompt, task message, and repository instruction files. They do not get the lead history. [2][3][8][11][12][16]
- The lead must write a specific task: objective, output format, boundaries. [1][7][11]
- Standing project context goes to every worker as a short brief. Memory goes as an index that the worker reads on demand. [4][18]
- Full-history forks exist, but reports show role confusion. [2][9][10]
- A reviewer works better without the author context. [7]
- More context costs tokens and does not reliably increase quality. [18][19][20][21]

## Sources
1. https://www.anthropic.com/engineering/multi-agent-research-system
2. https://code.claude.com/docs/en/sub-agents
3. https://code.claude.com/docs/en/agent-teams
4. https://code.claude.com/docs/en/claude-projects
5. https://cognition.com/blog/dont-build-multi-agents
6. https://cognition.com/blog/devin-can-now-manage-devins
7. https://cognition.com/blog/multi-agents-working
8. https://learn.chatgpt.com/docs/agent-configuration/subagents
9. https://github.com/openai/codex/issues/20077
10. https://github.com/openai/codex/issues/24150
11. https://cursor.com/docs/subagents
12. https://github.com/OpenHands/software-agent-sdk/blob/main/examples/01_standalone_sdk/41_task_tool_set.py
13. https://docs.github.com/en/copilot/how-tos/use-copilot-agents/cloud-agent/use-cloud-agent-on-github
14. https://jules.google/docs/
15. https://docs.factory.ai/missions/reference
16. https://ampcode.com/docs/models-and-subagents
17. https://ampcode.com/docs/orbs/agent-to-agent
18. https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
19. https://www.trychroma.com/research/context-rot
20. https://aclanthology.org/2024.tacl-1.9/
21. https://arxiv.org/abs/2602.11988
