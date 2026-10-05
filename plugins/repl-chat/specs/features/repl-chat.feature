Feature: repl-chat session protocol

  Rule: Basic commands

    Scenario: Help names the commands
      Given a session with nickname "agent"
      When the user enters ":help"
      Then the reply names ":help" and ":quit"
      And the reply does not end the session

    Scenario: Quit aliases end the session
      Given a session with nickname "agent"
      When the user enters ":q"
      Then the reply ends the session

    Scenario: Blank line gets no response
      Given a session with nickname "agent"
      When the user enters a blank line
      Then the reply has no text
      And the reply does not end the session

  Rule: Chat turns

    Scenario: One response per turn
      Given a session with nickname "agent"
      When the user enters "hello"
      Then the reply has exactly one line
      And the reply echoes "hello"

    Scenario: Only chat turns advance the counter
      Given a session with nickname "agent"
      When the user enters ":help"
      And the user enters a blank line
      And the user enters "first"
      Then the reply names turn 1
      When the user enters "second"
      Then the reply names turn 2

  Rule: Session lifecycle

    Scenario: The guest announces and closes the session
      Given the system is composed with the five plugins
      When the session ends
      Then the transcript announces "repl:" at the start
      And the transcript reports "repl: session closed" on unload
