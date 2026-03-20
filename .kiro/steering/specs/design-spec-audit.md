---
inclusion: manual
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description that will apply across all your workspaces.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

Perform a comprehensive technical audit of the design spec against core architectural pillars: Correctness, Scalability, Modularity, Reliability, and Efficiency. provide constructive criticism for each pillar, backed by a strong rationale or technical justification. For every critique, propose a concrete, idiomatic improvement that aligns with modern engineering best practices. It is important that your criticism is more balanced between the modern engineering best practices and the core platform principles.

# design.md
**Purpose:** Defines HOW the system will work

- Architecture and component structure
- Data models and interfaces
- Correctness properties (for property-based testing)
- Error handling strategies
- Testing approach
- Implementation decisions and rationales
- Example: "We'll use a ValidationService class with a validate() method that returns Result<T, ValidationError>"

Finally provide the percentage of design.md complaince to this audit.