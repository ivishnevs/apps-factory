## ADDED Requirements

### Requirement: No duplicate versions
Registering an artifact version whose content equals the artifact's latest version SHALL NOT create a new version;
Core SHALL answer with the existing version and say it is unchanged.

#### Scenario: Same sheet uploaded again
- **WHEN** an agent re-uploads a sheet byte-identical to the latest version
- **THEN** no new version SHALL be created and the answer SHALL name the existing version

### Requirement: Superseded approval requests close
When a newer version of an artifact is submitted for owner approval, open approval requests of its older versions
SHALL be closed as superseded, removed from the Inbox, and the waiting agent SHALL be told.

#### Scenario: v4 after v3
- **WHEN** the concepts v3 approval is open and v4 is submitted for approval
- **THEN** the v3 request SHALL be closed as superseded by v4 and only v4 SHALL be in the Inbox

### Requirement: Archive images in the Library
Images inside a zip or tar artifact SHALL be listed in the Library's Images as part of that archive version, after
checking the archive for unsafe paths and within size and count limits.

#### Scenario: Concept pack
- **WHEN** a zip with 73 PNG files is registered
- **THEN** Library → Images SHALL show those images, each marked with the archive it comes from
