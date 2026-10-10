Feature: Message list

  As the writer
  I want messages with their senders in a scrollable, selectable list
  so that I can read a conversation and copy any message in full

  Background:
    Given nvchat is running
    And "Chat one" is the open chat

  Rule: Every message shows its sender and body

    Scenario: Opening a chat renders its messages
      When the writer opens "Chat two"
      Then the message list shows the messages of "Chat two"
      And each message shows its sender and its body
      And the newest message is in view

  Rule: Selection is a whole message block

    Scenario: Selecting a message
      When the writer moves the cursor to a message
      Then the whole message block is selected
      And the selection includes the sender row

    Scenario: Yanking a selected message
      Given a message is selected
      When the writer yanks the selection
      Then the yank contains the sender row and the whole body

    Scenario: Yanking a range of messages
      Given messages are selected across several blocks
      When the writer yanks the selection
      Then the yank contains every covered block in order
      And each covered block keeps its sender row and its whole body

    Scenario: Yanking the whole conversation
      Given the whole message list is selected
      When the writer yanks the selection
      Then the yank contains every message of the open chat

  Rule: New messages never steal the reading position

    Scenario: A reply arrives while the newest message is in view
      Given the newest message is in view
      When a reply arrives for "Chat one"
      Then the reply is appended to the message list
      And the reply is in view

    Scenario: A reply arrives while the writer has scrolled up
      Given the writer has scrolled away from the newest message
      When a reply arrives for "Chat one"
      Then the reply is appended to the message list
      And the view is where the writer left it

  Rule: Job state changes join the transcript

    Scenario: A job changes state while a chat is open
      When a job started by the open chat changes state
      Then a system entry is appended to the message list
      And the entry's sender is "system"
