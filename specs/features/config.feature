Feature: The .core configuration layer

  core-agent reads its configuration from a .core/ directory: a user scope
  (~/.core, or CORE_DIR when set) and an optional project scope (./.core). The
  files layer per key over embedded defaults, known files are validated, and
  credentials never leave the host. These scenarios exercise the host-testable
  config library; the entry's end-to-end test reaches the same layer through a
  session.

  Rule: Discovery and precedence

    Scenario: The user directory supplies the configuration
      Given a user config directory with the shipped defaults
      When the configuration is loaded
      Then the loaded nick is "agent"
      And the loaded providers name "ollama"

    Scenario: CORE_DIR relocates the user directory
      Given a prepared config directory outside the home directory
      And the environment sets CORE_DIR to it
      When the configuration is loaded
      Then the loaded configuration comes from that directory

    Scenario: The project file overrides the user file per key
      Given a user config with nick "agent" and context budget 4096
      And a project config with nick "project"
      When the configuration is loaded
      Then the loaded nick is "project"
      And the loaded context budget is 4096

    Scenario: The environment overrides the files
      Given a user config with nick "agent"
      And the environment sets CORE_NICK to "env-nick"
      When the configuration is loaded
      Then the loaded nick is "env-nick"

  Rule: Validation

    Scenario: A malformed known file fails the load naming the file
      Given a user config whose settings.json is malformed
      When the configuration is loaded
      Then loading fails with an error naming "settings.json"

    Scenario: Unknown files and keys are ignored
      Given a user config carrying an unknown notes.txt
      And a settings.json with an unknown key "notes"
      When the configuration is loaded
      Then loading succeeds and the unknown key is ignored

    Scenario: Missing files fall back to the embedded defaults
      Given an empty user config directory
      When the configuration is loaded
      Then the loaded configuration is the shipped defaults

  Rule: Payloads

    Scenario: The merged configuration splits into the six payloads
      Given a complete shipped configuration
      When the configuration is split into payloads
      Then the providers payload lists "ollama"
      And the models payload resolves "fast" to "gemma4:cloud"
      And the agent payload names conversation "main" and model "fast"
      And the context payload budgets 4096
      And the history payload uses conversation "main"
      And the repl payload carries nick "agent"

  Rule: First run

    Scenario: First run seeds the defaults
      Given an empty user config directory
      When the configuration layer prepares it
      Then settings.json, providers.json, and models.json hold the shipped defaults
      And auth.json exists with mode 0600 and no credentials

    Scenario: Seeding never overwrites an existing file
      Given a user config whose settings.json customizes the nick to "kept"
      When the configuration layer prepares it
      Then settings.json still sets the nick to "kept"

  Rule: Credentials

    Scenario: An auth: reference resolves from auth.json
      Given an auth store holding "sk-auth" under "ollama"
      And a provider referencing "auth:ollama"
      When the host resolves the reference
      Then the resolved secret is "sk-auth"

    Scenario: The environment shadows a named credential
      Given an auth store holding "sk-auth" under "ollama"
      And the environment defines "ollama" as "sk-env"
      When the host resolves the reference "auth:ollama"
      Then the resolved secret is "sk-env"

    Scenario: A guest holds only the reference
      Given an auth store holding "sk-secret" under "ollama"
      And a provider referencing "auth:ollama"
      When the provider document is handed to the guest
      Then the document carries "auth:ollama" and no secret
