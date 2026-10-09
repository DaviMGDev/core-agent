Feature: Files panel

  As the writer
  I want a files panel and real editor windows
  so that nvchat is also where I work on files

  Background:
    Given nvchat is running

  Rule: The panel browses the filesystem

    Scenario: Opening the files panel
      Given the files panel is hidden
      When the writer opens the files panel
      Then the files panel is shown

    Scenario: Opening a file
      Given the files panel is shown
      When the writer chooses a file in the files panel
      Then the file is open in an editor window

  Rule: The chat is placed automatically

    Scenario: Opening a file gives the chat a column
      Given the chat fills the main area
      When the writer opens a file
      Then the chat is a right column beside the editor
      And the file is in view

    Scenario: Closing the last file returns the chat to full width
      Given a file is open in an editor window
      And the chat is a right column
      When the writer closes the editor window
      Then the chat fills the main area

    Scenario: Hiding the chat
      Given the chat is visible
      When the writer hides the chat
      Then the editor takes the full main area

    Scenario: Showing the chat again from the composer
      Given the chat is hidden
      And a file is open in an editor window
      When the writer focuses the composer
      Then the chat is visible
      And the chat is a right column beside the editor
