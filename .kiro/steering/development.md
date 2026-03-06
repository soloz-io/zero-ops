# REQUIREMENT PROMPTS

- Initiate a rigorous discovery phase using a question-driven approach:
  1. **Inquiry**: Ask a series of targeted Yes/No questions to validate requirements.
  2. **Assumptions**: For every question, explicitly state the technical or functional assumption being made.
  3. **Blocking**: Proceed to implementation ONLY after receiving 100% confirmation on all answers.
  4. **Strictness**: Avoid over-engineering or deviating from confirmed logic. Keep all communication objective and concise.

- read this SPEC .md files and with given understanding, interview me in detail about literally anything: technical implementation, UI & UX, concerns, tradeoffs, etc. but make sure the questions are not obvious. be very in-depth and continue

- Could you include wireframe diagrams in ascii for the screens we're going to build as well?

- is this the best way to do this? I'm not familiar with the technology, please refer to the docs as necessary to build this in an idiomatic way.

- @bizmatters/.kiro/steering/task-spec-format.md:10-76  i want you to keep teh context as it is. Do not change teh contest or its meaning. But organize and articulate in a way better understandable.

- use chrome dev tools mcp.
first take snapshot, analayse error and keep your response consice, to the point in less then 50 lines ..
do not implement anything. just report. 
never create a implementation sumamry.
Do not run commands in isolation. Always find the root cause and fix the issues in projetc files first.
url - 

- Always keep simple technical terms for discussion. Dont bring any analogy during discussion.

- i want you to help me define the flexible mental model that in a few ways it can be made more fluid and useful for an AI agent (less “if this → then that” rigidness, more mind-mapping heuristics). The factor should help itself mindmap. I dont want a strict rules to be set. 

- Conduct a rigorous GAP analysis of the requirement specs. Identify missing details, logical inconsistencies, or unanswered questions likely to be raised during product refinement. Focus on: edge cases, dependency gaps, and undefined error states. Provide specific, actionable questions for the product team to ensure the spec is "Development Ready".

- Perform a comprehensive technical audit of the design spec against core architectural pillars: Correctness, Scalability, Modularity, Reliability, and Efficiency. provide constructive criticism for each pillar, backed by a strong rationale or technical justification. For every critique, propose a concrete, idiomatic improvement that aligns with modern engineering best practices.

- first we will start with B. how do i ask another code gen model to generate a scaffold as per our expectation and then get the task done one by one. i need a detailed instruction to kick start the UI engine scaffold. i want you to give me instruction to get the jobs done from another coding agent. i will keep sharing the outcome. you have to suggest next steps

- I was trying to combine the v1 and v2 version of the frd and created the v3 version. I want you to review the v3 version if it misses any of the core context from v1 and v2. If there are any improvements that you feel that can be provided, then feel free and give constructive critisim for how to improve it? you need to provide reason/justification for your critisim and suggested improvement?

- I want you to review the spec documents. Analyse the files for any contradictory specs in the files. The expectation is that all the specs should align to single principles and should not deviate from one another. If there are any improvements that you feel that can be provided, then feel free and give constructive critisim for how to improve it? you need to provide reason/justification for your critisim and suggested improvement?

- Great. Here now i am sharing the SSD document of v1 and v2. 
Keeping the V4 FRD version as spec, can you create a SSD for the combined FRD. 

- Great. can you generate a user journey document for the combined FRD. I want this user journey to cover most of the possible user flows. I am sharing a sample user flow which is incomplete and can vary from the combined FRD. But your task is to create a document that captures all user journeys. Thsi will help product team to understand and if any of user flows that are left addresed.

# TDD PROMPTS

- Report me after completing the test cases. Use given dir as working and output dir for writing TDD. Completes the test cases first and then proceed with test cases to make sure the test cases are generating outcomes as expected. The test cases shall cover only testing the new artifact generations. No complex testing needed. If required look at other test cases in the project as reference. Backend testing dir - "/Users/arun_subramanian/Projects/ai/browser_automation/oranger/apps/backend/tests/test_workspace/" . Frontend testing dir - "/Users/arun_subramanian/.oranger/workspace/mock-working-dir".

- you should never add fallback or mock the instructions. Do not deviate from actual logic. Do not deviate or over-engineer the solution. I would expect the test cases to fail if there are issues or during its first run as we are following TDD and implementations will be missing which is known already. Do not bloat or deviate from actual logics. Add only what is required.  If necesary implementations are missing, then before creating the test files, get my approval and build the missing implementations. Test cases should not enrich or enhance the value produced by the source file. It should write or display the artifacts as it is produced.

- You should create test cases replicating the src folder. I mean if there is a src/services folder, then service relates test cases must also be under tests/src/services/ folder. Also your test cases must cover only the outcome driven evaluations, where you should only validate the different artifacts generated by the testing files. Not not add any additional test cases. Always use th this as your testing dir - tests/test_workspace/

- Do not execute the test cases. Update me once you fix the issue. I will run the test cases and update you’re the results. First have enough console logs before testing. since its taking long time for test to run all cases, make sure you get enough data before running test cases.   

# DEVELOPMENT PROMPTS

- Make use of backend service agent for implementing the phase1.6, task 6 tasks as these are related to its skills. 
backend-services-developer should refer to the provided skills related to the 
task before implementing to be aligned on the project patterns and expectations.
Report me if it lacks any of skills required for completing the task. 
proceed with task implementations one by one to complete all the tasks and update each task (not sub-tasks) immediately after completion with small implementation notes. 
Stick to the task order.
Report me when you are stuck. 

-  Developer Workflow:
  - First-time: sudo ./setup.sh (once only, fully automated)
  - Daily: ./scripts/services.sh start (fast, lightweight)
  - Manage: ./scripts/services.sh [start|stop|restart|status|logs] [service]
  - Rebuild: ./scripts/clean-infra.sh && sudo ./setup.sh

- use setup.sh to run the Run the infrastructure deployment. Make sure you follow same instrcutions that you have given other dev in contribution.

- you should go through all the components before developing new ones. update the implementation-spec if there are resuabale componets from main project. Try to make use of most and avoid duplicate componets.

- If any changes agreed in functional spec or in implmenettaion spec, the related spec document should be kept updated with latest decision upon getting approval.

- go through all the spec and tell me when you are ready -  /Users/arun_subramanian/Projects/ai/browser_automation/oranger/apps/frontend/docs/editor/spec/

- Great. With the given understanding, can you now create a implmentation spec here for this "Youtube Video Analyser Agent". Do not create seperate sections for preformance, reliability, scalability. Focus only on the project structure, project files and following existing patterns in the mono repo project.

- Great. Lets start implementing tools first. First tool to implement is the YouTubeSearcher tool. can you craete the tool spec in this folder? - /Users/arun_subramanian/Projects/ai/browser_automation/oranger/apps/tools-base/src/tools/specs/

- Identify missing depencies for tool implementation. Get the approval to build and test the depedencies first before proceeding with tool implmentation.

- create a development todo list for implementaion first before proceeding.

- you should never add fallback or mock the instructions. Do no not deviate from actual logic. Do not deviate or over-engineer the solution.

# TESTING PROMPTS

-   the test case should be stable enough to be executed in isolated matter. it
  should check for the serivices and get started by itself without having to check
  and exeucte certain bash commands each time. Use the scripts/ folder to create
  any scripts required. but make sure you dont craete any redundant scripst and try
  to reuse existing scripst if existed. create only if you dont have any scripts
  existing fr given use case. 
if a command needs to be executed during testing, update and use the setup script.

- you should not use the utils directly. The test should import the services and the service should be using utils. 

- Do not to make any changes in utils data. The services needs to just return what was producded by utils. Check services you created if they are modyfing the util reponses.

- Do not execute the test cases. Update me once you fix the issue. I have run the test cases on your behalf. you can find the logs here - /Users/arun_subramanian/Projects/ai/browser_automation/oranger/apps/agents-base/tests/logs/video_processor.md

- issue still there - Here is the console log from loading, start, pause. - /Users/arun_subramanian/Projects/ai/browser_automation/oranger/apps/frontend/tests/logs/console.log