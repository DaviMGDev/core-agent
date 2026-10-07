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
