Feature: Agent — the turn loop

  A turn runs from wake to quiescence: the model may answer, start a job, or
  say nothing; at most one chat.message leaves a turn; every agent works on
  its own context; presentation is the agent's.

  Rule: A turn runs from wake to quiescence

    Scenario: A turn yields one message
      Given an agent whose model answers "hello"
      When a user line wakes the agent
      Then the turn yields exactly one chat.message "hello"

    Scenario: Silence is a legitimate turn
      Given an agent whose model stays silent
      When a user line wakes the agent
      Then the turn yields no message

    Scenario: A tool call ends the turn without a message
      Given an agent whose model calls the tool "read"
      When a user line wakes the agent
      Then the turn starts one job for "read"
      And the turn yields no message

  Rule: Every agent works on its own context

    Scenario: The turn uses its own conversation and budget
      Given an agent with conversation "side" and budget 3
      When a user line wakes the agent
      Then the pipeline asks for 3 recent turns of "side"

  Rule: Presentation is the agent's

    Scenario: The model sees the presented tools only
      Given an agent with tools "read" and "write"
      When the tools are presented
      Then the model sees "read" and "write"

    Scenario: Hidden tools are pruned and renamed tools relabeled
      Given an agent with tools "read" and "bash", hiding "bash" and renaming "read" to "load"
      When the tools are presented
      Then the model sees "load" only
