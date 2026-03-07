---
inclusion: manual
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description that will apply across all your workspaces.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

Create a comprehensive summary of the received or transferred conversation and save it to a file named 'chat-logs/DD-MM-YYYY/chat-HH.md' where DD-MM-YYYY is the current date folder and HH is the current hour in 24-hour format (e.g., chat-logs/07-03-2026/chat-14.md for 2pm, chat-logs/07-03-2026/chat-18.md for 6pm). 

CRITICAL: Use the actual current hour number (00-23) in the filename, not the literal text "chat-hour".

FILE HANDLING:
- If the file already exists for the current hour, APPEND to it instead of overwriting
- When appending, add new content to existing sections (do NOT duplicate section headers)
- Append new entries chronologically within each section
- Add a separator (e.g., "---") between different conversation summaries within the same hour

Include: 
1) A brief overview of the conversation topic and goals, 
2) Key decisions made and their rationale, 
3) Important code changes, file modifications, or configurations applied, 
4) Any unresolved issues or follow-up items, 
5) Full message history with user requests and assistant responses. Format as markdown with clear sections and preserve technical details, commands, and code snippets.