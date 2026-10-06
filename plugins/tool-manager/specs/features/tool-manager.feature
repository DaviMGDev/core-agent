Feature: Tool manager — every call is a job

  The registry holds what can be called; a call is a job with a life the
  caller can observe and stop.

  Rule: The registry holds what can be called

    Scenario: A tool is declared
      Given an empty tool registry
      When a tool "read" with description "read a file" is declared
      Then the registry lists "read"

    Scenario: A duplicate tool name is refused
      Given a registry with tool "read"
      When another tool "read" is declared
      Then the declaration fails
      And the registry still lists one tool

  Rule: A job's life

    Scenario: start returns a handle without waiting
      Given a manager with a blocking tool "slow"
      When the tool "slow" is started
      Then the call returns a job handle before the tool finishes

    Scenario: A job reaches done
      Given a manager with tool "echo" answering "hi"
      When the tool "echo" is started
      Then the job reaches done with result "hi"

    Scenario: A job reaches failed
      Given a manager with a failing tool "boom"
      When the tool "boom" is started
      Then the job reaches failed with an error

    Scenario: A refused start fails at once with the reason
      Given a manager with no tools
      When the tool "missing" is started
      Then the job fails at once with reason "unknown tool"

    Scenario: peep returns state, last tick, and output so far
      Given a manager with a tool "chatty" that writes and blocks
      When the tool "chatty" is started and writes
      Then peep reports state "running" and the output so far

  Rule: Ticks and terminal events

    Scenario: A running job ticks with elapsed
      Given a manager with a 10ms tick and a blocking tool "slow"
      When the tool "slow" is started
      Then a job.tick event carries elapsed time

    Scenario: A job emits completed when it ends
      Given a manager with tool "echo" answering "done"
      When the tool "echo" is started
      Then a job.completed event carries the result "done"

  Rule: Kill and reclamation

    Scenario: kill is accepted at any point and emits job.killed
      Given a manager with a blocking tool "slow"
      And the tool "slow" is started
      When the running job is killed
      Then the job is killed at once
      And a job.killed event is emitted

    Scenario: unloading the manager reclaims its jobs without a kill
      Given a manager with a blocking tool "slow"
      And the tool "slow" is started
      When the manager unloads
      Then the job fails with "tool manager closed"
      And no job.killed event is emitted
