Feature: Composer

  As the writer
  I want to write in a normal Neovim buffer
  so that editing a message feels like editing any other text

  Background:
    Given nvchat is running
    And "Chat one" is the open chat

  Rule: The draft is edited with normal Neovim editing

    Scenario: Typing into an empty draft
      Given the composer is empty
      And the composer is focused
      When the writer types "hello"
      Then the draft is "hello"

    Scenario: Using a Neovim motion in the draft
      Given the composer holds "hello world"
      And the composer is focused
      When the writer deletes the word under the cursor
      Then the draft is "hello "

    Scenario: Leaving and returning keeps the draft
      Given the composer holds "half a message"
      When the writer leaves the composer
      And the writer returns to the composer
      Then the draft is "half a message"

  Rule: The composer never blocks quitting

    Scenario: Erasing the draft clears the modification
      Given the composer holds "half a message"
      And the composer is focused
      When the writer erases the draft
      Then the composer is empty
      And the editor can quit without warnings

    Scenario: Quitting with an unsent draft
      Given the composer holds "half a message"
      And the composer is focused
      When the writer quits nvchat
      Then the editor quits without warnings

  Rule: An unsent draft survives quitting

    Scenario: Quitting with a draft and reopening
      Given the composer holds "half a message"
      And the composer is focused
      When the writer quits nvchat
      And the writer launches nvchat again
      Then the draft is "half a message"
      And the composer is focused

    Scenario: Drafts belong to their session
      Given the composer holds "half a message"
      When the writer opens "Chat two"
      Then the composer is empty
      When the writer types "a second draft"
      And the writer opens "Chat one"
      Then the draft is "half a message"

  Rule: Enter sends; a line break takes an explicit key

    Scenario: Pressing Enter sends the draft
      Given the composer holds "hello"
      And the composer is focused
      When the writer presses Enter
      Then the message list ends with a message from "you"
      And the message body is "hello"
      And the composer is empty
      And the composer is focused

    Scenario: Inserting a line break instead of sending
      Given the composer holds "hello"
      And the composer is focused
      When the writer presses ctrl+j
      Then the draft is two lines
      And the message list is unchanged

    Scenario: Sending a multi-line draft
      Given the composer holds "hello"
      And the composer is focused
      When the writer presses ctrl+j
      And the writer types "world"
      And the writer presses Enter
      Then the message list ends with a message from "you"
      And the message body has two lines
      And the composer is empty

  Rule: Sending hands the draft to the session

    Scenario: Sending a non-empty draft
      Given the composer holds "hello"
      When the writer sends the draft
      Then the message list ends with a message from "you"
      And the message body is "hello"
      And the composer is empty
      And the composer is focused

    Scenario: Sending an empty draft
      Given the composer is empty
      When the writer sends the draft
      Then the message list is unchanged
      And the composer is focused
