Feature: model-manager views and resolution

  Rule: Registration

    Scenario: A view needs exactly one mode
      Given an empty model registry
      When a view "broken" sets both alias and fallback
      Then registration fails

    Scenario: A duplicate view name is refused
      Given a registry with view "fast" as alias of "gpt"
      When another view "fast" is registered
      Then registration fails

  Rule: Resolution

    Scenario: A plain model resolves to itself
      Given an empty model registry
      When the name "gpt" is resolved
      Then the resolution mode is "model"
      And the targets are ["gpt"]

    Scenario: An alias resolves to its target
      Given a registry with view "fast" as alias of "gpt"
      When the name "fast" is resolved
      Then the targets are ["gpt"]

    Scenario: A fallback resolves in order
      Given a registry with view "reliable" as fallback ["a", "b"]
      When the name "reliable" is resolved
      Then the resolution mode is "fallback"
      And the targets are ["a", "b"]

    Scenario: A discussion resolves participants in order
      Given a registry with view "panel" as discuss ["a", "b", "c"]
      When the name "panel" is resolved
      Then the resolution mode is "discuss"
      And the targets are ["a", "b", "c"]

    Scenario: Nested views flatten
      Given a registry with view "fast" as alias of "gpt"
      And a view "reliable" as fallback ["fast", "local"]
      When the name "reliable" is resolved
      Then the targets are ["gpt", "local"]

  Rule: Cycles are refused

    Scenario: Self-reference is refused
      Given a registry with view "loop" as alias of "loop"
      When the name "loop" is resolved
      Then resolution fails with a cycle error

    Scenario: An alias cycle is refused
      Given a registry with view "a" as alias of "b"
      And a view "b" as alias of "a"
      When the name "a" is resolved
      Then resolution fails with a cycle error

  Rule: Response answers through the caller

    Scenario: Responding answers with the resolved model
      Given a registry with view "fast" as alias of "gpt"
      When the model "fast" is asked to respond
      Then the answer comes from "gpt"

    Scenario: A fallback answers with its first working target
      Given a registry with view "reliable" as fallback ["a", "b"]
      And the model caller fails for "a"
      When the model "reliable" is asked to respond
      Then the answer comes from "b"

    Scenario: Responding without a caller is refused
      Given a registry with view "fast" as alias of "gpt"
      And no model caller is configured
      When the model "fast" is asked to respond
      Then the response fails

    Scenario: The mock caller echoes the model and the context size
      Given a registry with view "fast" as alias of "gpt"
      When the model "fast" is asked to respond
      Then the answer comes from "gpt"
      And the answer text is "mock(gpt) (context:0)"
