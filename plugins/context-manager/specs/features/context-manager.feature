Feature: context-manager projection

  Rule: Projection fits the budget

    Scenario: Exact fit keeps every message
      Given a history with messages "aa", "bb" and budget 4
      When the window is projected
      Then the window keeps ["aa", "bb"]
      And no messages are dropped

    Scenario: Over budget drops the oldest
      Given a history with messages "aaa", "bbb", "cc" and budget 5
      When the window is projected
      Then the window keeps ["bbb", "cc"]
      And one message is dropped

    Scenario: The newest message is kept even when oversized
      Given a history with messages "old", "very long newest" and budget 4
      When the window is projected
      Then the window keeps ["very long newest"]
      And one message is dropped

    Scenario: Empty history projects an empty window
      Given an empty history and budget 100
      When the window is projected
      Then the window is empty
      And no messages are dropped

    Scenario: A non-positive budget keeps only the newest
      Given a history with messages "old", "newest" and budget 0
      When the window is projected
      Then the window keeps ["newest"]
      And one message is dropped
