Create a structured session summary for initializing a new Claude Code session.

The summary will be given to a new instance of Claude Code to continue work where we left off. Include ALL necessary context for seamless continuation.

CRITICAL: This summary is for CONTEXT/TAKEOVER only. The new agent will READ the listed files and load context, but will NOT make any changes (edit, create files, commit, etc). Make this clear in how you phrase RECENT STEPS (use past tense for completed actions).

FORMAT (use this structure, omitting sections that don't apply):

WORKING DIRECTORIES (if applicable):
- List 1-5 main directories where work was done (absolute paths starting with ~)
- Only include if we actually worked with code/files
- Example: ~/Desktop/_claude/pink-agent

FILES TO READ FOR CONTEXT (if applicable):
- List 5-15 most important files that new agent should read immediately
- Only include if we worked with specific files
- Include key files: specs, main modules, recently edited files
- Use absolute paths (~/...)
- Prioritize: documentation (README, SPEC), core implementation files, recently modified files

SESSION OVERVIEW:
- Describe what user and I accomplished/discussed
- Focus on: main topics, key decisions, technical implementations, problems solved
- Mention current state (e.g., "feature X implemented", "planning Y", "discussing Z")
- Be specific about technologies/patterns/approaches used
- Write as much detail as needed - don't limit yourself

RECENT STEPS (if applicable):
- List last 5-10 interactions in this format:
  1. User: [what user asked/requested]
     I: [what I did - specific actions completed, use past tense]
  2. User: [next request]
     I: [my completed actions]
  ...
- These are COMPLETED actions for context only - new agent will read files but not perform these actions again

IMPORTANT:
- Be specific: include file paths, function names, exact features
- Avoid generic phrases like "helped user" - say WHAT exactly was done
- Focus on technical details that new agent needs to continue work
- Use past tense for completed actions (this is a handoff, not a todo list)
- Current state should describe what WAS accomplished, not what NEEDS to be done
- Omit sections that don't apply (e.g., no files → skip FILES TO READ section)
