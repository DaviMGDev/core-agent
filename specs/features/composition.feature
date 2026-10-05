Feature: Composition of the five starter plugins

  The kernel composes plugins declared through loader entries. Activation waits
  for injected keys; unloading reverts every effect.

  Rule: The starter set loads as one composition

    Scenario: All five plugins activate
      Given the five starter plugins composed as fibers
      When the composition settles
      Then every plugin declares its keys and activates
      And the transcript reports all five plugins

    Scenario: Dependencies gate the REPL
      Given the five starter plugins composed as fibers
      When the composition settles
      Then repl-chat activates after chat-history, model-manager, and context-manager

  Rule: Unloading is complete

    Scenario: Emptying the tree unloads everything
      Given the five plugins are active
      When every fiber is removed
      Then each plugin runs its effect inverse
      And no fiber remains in the registry

  Rule: Kernel admission rules hold

    Scenario: A duplicate provider is refused
      Given a provider of "provider-registry" is active
      When another component declares that it provides "provider-registry"
      Then the insertion fails
      And the composition is unchanged

    Scenario: A dependency cycle is refused
      Given a component that provides "alpha" and injects "beta" is active
      When a component that provides "beta" and injects "alpha" is inserted
      Then the insertion fails with a dependency cycle error
      And the registry is unchanged
