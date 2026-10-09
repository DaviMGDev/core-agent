Feature: Keyboard navigation

  As the writer
  I want every region reachable from the keyboard
  so that I never need the mouse to move around the workspace

  Background:
    Given nvchat is running

  Rule: Focus moves between the regions

    Scenario: Focusing the files panel
      Given the composer is focused
      When the writer focuses the files panel
      Then the files panel has focus

    Scenario: Focusing the message list
      Given the composer is focused
      When the writer focuses the message list
      Then the message list has focus

    Scenario: Focusing the composer
      Given the message list has focus
      When the writer focuses the composer
      Then the composer has focus

    Scenario: Focusing a hidden files panel opens it
      Given the files panel is hidden
      When the writer focuses the files panel
      Then the files panel is shown
      And the files panel has focus

  Rule: The panel and the overlay toggle from the keyboard

    Scenario: Toggling the files panel with mod+e
      Given the files panel is shown
      When the writer presses mod+e
      Then the files panel is hidden
      When the writer presses mod+e
      Then the files panel is shown

    Scenario: Toggling the sessions overlay with mod+s
      Given the sessions overlay is hidden
      When the writer presses mod+s
      Then the sessions overlay is open
      When the writer presses mod+s
      Then the sessions overlay is hidden
