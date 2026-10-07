Feature: Subagent — an agent callable as a tool

  A subagent is an agent the manager calls: the call is a job like any
  other, the child is isolated, and the reply comes back.

  Rule: A subagent call is a job like any other

    Scenario: A subagent call reaches done with the child's reply
      Given a manager with a subagent tool
      When a subagent is started with the brief "summarize the log"
      Then the subagent job reaches done with reply "child: summarize the log"

    Scenario: A subagent call can be observed and killed
      Given a manager with a blocking subagent tool
      When a subagent is started with the brief "wait"
      Then peep reports the subagent job running
      When the subagent job is killed
      Then the subagent job is killed at once

  Rule: The child is isolated

    Scenario: The child's first wake carries only the brief
      Given a manager with a subagent tool
      When a subagent is started with the brief "count the files"
      Then the child's first wake carries only "count the files"

    Scenario: The child's conversation is its own
      Given a manager with a subagent tool
      When a subagent is started with the brief "count the files"
      Then the child's conversation is its own

    Scenario: Two calls do not share a conversation
      Given a manager with a subagent tool
      When a subagent is started with the brief "first"
      And a subagent is started with the brief "second"
      Then the two calls use different conversations

  Rule: The caller chooses the return shape

    Scenario: The default returns only the final reply
      Given a manager with a subagent tool
      When a subagent is started with the brief "summarize"
      Then the subagent job reaches done with reply "child: summarize"
      And the result carries no conversation

    Scenario: The caller can ask for the whole conversation
      Given a manager with a subagent tool
      When a subagent is started with the brief "summarize" asking for the conversation
      Then the subagent job reaches done with reply "child: summarize"
      And the result carries the child's conversation

  Rule: The child's model is configuration

    Scenario: The child runs on the configured default model
      Given a manager with a subagent tool configured with model "reliable"
      When a subagent is started with the brief "summarize"
      Then the child's wake names the model "reliable"

    Scenario: A per-call model overrides the default
      Given a manager with a subagent tool configured with model "reliable"
      When a subagent is started with the brief "summarize" on model "fast"
      Then the child's wake names the model "fast"

    Scenario: The child's model view is resolved like any agent
      Given a manager with a subagent tool resolving models
      When a subagent is started with the brief "summarize" on model "reliable"
      Then the subagent job reaches done with reply "mock(llama-3.2): summarize (context:1)"

  Rule: The job tree is bounded

    Scenario: A start past the depth bound fails like any failed job
      Given a manager with a recursing subagent tool
      When the subagent chain is started
      Then the innermost job fails with a depth reason

  Rule: The parent is the sole listener

    Scenario: A child's events reach the parent, not the bus
      Given a manager with a parent job and a child job
      When the child job completes
      Then the parent's listener receives the child's events
      And the bus saw only the root job's start
