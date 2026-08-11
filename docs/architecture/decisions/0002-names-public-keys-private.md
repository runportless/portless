# ADR 0002: names are public; ownership keys are private

Status: accepted

Project routes use a readable machine-local name. Services, recordings, and faults use names within that project. Operations and traffic use project-local integer sequences.

SQLite generates immutable random ownership keys and the selected container engine labels resources with them. Those keys are absent from API schemas, URLs, CLI output, UI copy, and exported declarations. This preserves safe cleanup and rename semantics without making users manipulate opaque identifiers.
