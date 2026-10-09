Feature: Startup

  As the writer
  I want nvchat to open in the conversation I was last in
  so that I can continue without navigating first

  Background:
    Given nvchat is running

  Scenario Outline: Launching opens the most recent chat
    Given the store lists "Chat one", "Chat two" and "Chat three"
    And "<recent>" is the most recent chat
    When the writer launches nvchat
    Then "<recent>" is the open chat
    And the message list shows the messages of "<recent>"
    And the composer is focused

    Examples:
      | recent   |
      | Chat one |
      | Chat two |
      | Chat three |
