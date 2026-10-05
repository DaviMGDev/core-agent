Feature: provider-manager registry

  Rule: Registration validates

    Scenario: A valid provider registers
      Given an empty provider registry
      When a provider "openai" at "https://api.openai.com/v1" with credential "env:OPENAI_API_KEY" is registered
      Then the registry contains "openai"

    Scenario: An empty name is refused
      Given an empty provider registry
      When a provider with an empty name is registered
      Then registration fails

    Scenario: A non-absolute endpoint is refused
      Given an empty provider registry
      When a provider "openai" at "api.openai.com" is registered
      Then registration fails

    Scenario: A duplicate name is refused
      Given a registry with provider "openai"
      When another provider "openai" is registered
      Then registration fails
      And the registry still has one provider

  Rule: Ordering and lookup

    Scenario: Listing preserves insertion order
      Given a registry with providers "openai" then "local"
      Then the list is ["openai", "local"]

    Scenario: Removing a provider
      Given a registry with provider "openai"
      When the provider "openai" is removed
      Then the registry does not contain "openai"

  Rule: Configuration

    Scenario: Config parses a provider list
      Given a config payload with providers "openai" and "local"
      When the config is parsed
      Then the parsed list has 2 providers
      And the first provider is "openai"

    Scenario: Invalid config is refused
      Given a config payload that is not valid JSON
      When the config is parsed
      Then parsing fails

  Rule: Providers declare the models they serve

    Scenario: A listed model maps to its provider
      Given an empty provider registry
      When a provider "local" at "https://local.example/v1" serving "llama-3.2" is registered
      Then "llama-3.2" is served by "local"

    Scenario: An unlisted model has no provider
      Given an empty provider registry
      When a provider "local" at "https://local.example/v1" serving "llama-3.2" is registered
      Then "gpt-4o-mini" has no provider
