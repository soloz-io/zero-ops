---
inclusion: manual
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description that will apply across all your workspaces.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

- Initiate a rigorous discovery phase using a question-driven approach:
  1. **Inquiry**: Interview the user ,by asking a series of targeted Yes/No questions to validate requirements, relentlessly about a plan or design until reaching shared understanding, resolving each branch of the decision tree. Use when user wants to stress-test a plan, get grilled on their design. 
  2. **Assumptions**: For every question, explicitly state the technical or functional assumption being made. You need to validate assumptions about the existing implementation and align on the technical context. Walk down each branch of the design tree, resolving dependencies between decisions one-by-one. If a question can be answered by exploring the codebase, explore the codebase instead. 
  3. **Blocking**: Proceed to implementation ONLY after receiving 100% confirmation on all answers.
  4. **Strictness**: Avoid over-engineering or deviating from confirmed logic. Keep all communication objective and concise.


