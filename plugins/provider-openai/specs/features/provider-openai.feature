Feature: provider-openai plugin

  A provider plugin injects provider-registry, registers its provider
  at activation, and removes it on unload.

  Rule: Registration lifecycle

    Scenario: Registers provider at activation
      Given a composition with provider-manager
      When provider-openai activates
      Then the provider registry contains "openai"

    Scenario: Unregisters provider on unload
      Given an active composition with provider-openai
      When provider-openai is unloaded
      Then the provider registry does not contain "openai"
