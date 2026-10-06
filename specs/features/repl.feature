Feature: The REPL session

  The LLM user reaches the system through the REPL, like any other user. The
  session protocol is fixed. These host-level scenarios stand in the
  model-manager mock caller for the provider so the transcript stays
  assertable; the entry's end-to-end test exercises the real exchange.

  Rule: The session protocol

    Scenario: Session start
      Given the system is composed with the six plugins and nickname "agent"
      When the session starts
      Then the transcript announces "repl: agent joined"
      And the transcript shows the prompt "you> "

    Scenario: Help lists the basic commands
      Given a running session
      When the user enters ":help"
      Then the response names the commands ":help", ":quit"

    Scenario: A chat turn returns one response
      Given a running session
      When the user enters "hello"
      Then the turn returns exactly one response line

    Scenario: A blank line keeps the prompt
      Given a running session
      When the user enters a blank line
      Then no response line is produced for that turn

    Scenario: Quit ends the session
      Given a running session
      When the user enters ":quit"
      Then the session ends
      And unload runs the repl-chat inverse

  Rule: Session end variants

    Scenario: End of input ends the session
      Given a running session whose input is exhausted
      When the guest reaches end of input
      Then the session ends cleanly
