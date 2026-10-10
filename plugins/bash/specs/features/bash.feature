Feature: bash — a default shell tool

  One call is one job: the command streams while it runs, the result is
  factual, kill reaches the process tree, and the timeout bounds the call.

  Rule: One call is one job

    Scenario: A call runs a command and returns the factual result
      Given a bash tool on a manager
      When bash runs "printf out; printf err >&2"
      Then the bash job reaches done
      And the bash result carries exit code 0, stdout "out", and stderr "err"

    Scenario: peep shows output before the command finishes
      Given a bash tool on a manager
      When bash runs a command that blocks after writing "waiting"
      Then peep shows "waiting" while the bash job is running
      When the blocked command is released
      Then the bash job reaches done

    Scenario: A non-zero exit fails the job with the captured output
      Given a bash tool on a manager
      When bash runs "printf boom >&2; exit 3"
      Then the bash job reaches failed
      And the bash error carries "exit code 3" and "boom"

    Scenario: A killed call lands killed and its process tree is gone
      Given a bash tool on a manager
      When bash runs a command that spawns a descendant and waits
      And the bash job is killed
      Then the bash job is killed at once
      And the spawned descendant is gone

    Scenario: An expired timeout fails the job and terminates the process
      Given a bash tool on a manager
      When bash runs a command that hangs with timeout_ms 200
      Then the bash job reaches failed with a timeout error
      And the command's process is gone

  Rule: The shell is selected at the call

    Scenario: bash runs the command when it is present
      Given a bash tool resolving "bash"
      When bash runs "printf bashish"
      Then the bash result carries stdout "bashish"

    Scenario: sh is the fallback when bash is absent
      Given a bash tool resolving only "sh"
      When bash runs "printf portable"
      Then the bash result carries stdout "portable"
