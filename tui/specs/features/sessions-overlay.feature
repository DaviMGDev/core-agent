Feature: Sessions overlay

  As the writer
  I want the launch's chats in an overlay I can summon
  so that switching conversations never costs a permanent column

  Background:
    Given nvchat is running

  Rule: The overlay toggles without losing its place

    Scenario: Closing the overlay
      Given the sessions overlay is open
      When the writer closes the sessions overlay
      Then the sessions overlay is hidden
      And the workspace is unchanged

    Scenario: Reopening the overlay keeps its place
      Given the sessions overlay is open
      And the overlay cursor is on "Chat 1"
      When the writer closes the sessions overlay
      And the writer opens the sessions overlay
      Then the overlay cursor is on "Chat 1"

  Rule: The overlay lists the launch's chats flat

    Scenario: Listing chats
      Given the launch has the chats "Chat 1" and "Chat 2"
      When the writer opens the sessions overlay
      Then the overlay lists the chats flat, without threads

  Rule: Choosing a chat opens it

    Scenario: Opening another chat
      Given "Chat 1" is the open chat
      When the writer chooses "Chat 2"
      Then "Chat 2" is the open chat
      And the message list shows the messages of "Chat 2"
      And the sessions overlay is hidden

    Scenario: Creating a new chat
      When the writer chooses New chat
      Then a blank chat is created and opened
      And the sessions overlay is hidden
