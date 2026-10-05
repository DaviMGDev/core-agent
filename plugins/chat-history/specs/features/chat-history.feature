Feature: chat-history record

  Rule: Recording

    Scenario: Turns are recorded in order
      Given a conversation "main"
      When the user turn "hello" is appended
      And the assistant turn "hi" is appended
      Then the conversation has 2 turns
      And the first turn is "user: hello"
      And the second turn is "assistant: hi"

    Scenario: Unknown roles are refused
      Given a conversation "main"
      When a turn with role "system" is appended
      Then the append fails

    Scenario: Appending starts an unknown conversation
      Given an empty store
      When the user turn "hello" is appended to "main"
      Then the store lists ["main"]

  Rule: Serving

    Scenario: Recent returns the last n turns in order
      Given a conversation "main" with turns "one", "two", "three"
      When the last 2 turns are requested
      Then the turns are ["two", "three"]

    Scenario: Recent beyond the length returns everything
      Given a conversation "main" with turns "one", "two"
      When the last 5 turns are requested
      Then the turns are ["one", "two"]

    Scenario: Conversations list in creation order
      Given a store with conversations "main" then "side"
      Then the conversation list is ["main", "side"]
