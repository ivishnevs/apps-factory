## ADDED Requirements

### Requirement: People attach images to messages
A person who may write in a conversation (the owner or a member) SHALL be able to attach images to a message they
send there, from the web (button, paste, drag-and-drop), the CLI and Telegram replies. Files SHALL be uploaded to Core
first and named by the message; Core SHALL refuse an upload of another company, a type that is not an image, more
than 10 files or a file over 20 MB. A person's attachments SHALL be stored and shown like an agent's attachments.

#### Scenario: Screenshot to the CEO
- **WHEN** the owner pastes a screenshot into the chat with the CEO and sends "this is how the competitor does onboarding"
- **THEN** the message SHALL show the image inline in the chat, and the CEO's next run SHALL have the image in its worktree and its path in the brief next to the message

#### Scenario: Photo from Telegram
- **WHEN** the owner replies to the producer's question in Telegram with a photo captioned "like this"
- **THEN** the answer SHALL be "like this" with the photo attached

#### Scenario: Not an image
- **WHEN** a person tries to attach a ZIP archive
- **THEN** Core SHALL refuse the upload and say which types are accepted

### Requirement: Agents see what people attach
Before every run the node SHALL download from Core the attachments of the run's task (its comments and the messages
filed under it) or, for a conversation run, of the latest 20 people's messages to its agent in the project, into the
working directory under
`.factory/inputs/` before the runtime starts, and the brief SHALL list each file's path and caption next to its
message or comment. Core SHALL keep the originals; this SHALL hold for task runs and conversation runs, with or
without a repository, on whichever node the run lands.

#### Scenario: Conversation run
- **WHEN** the lead's conversation wakes on a person's message with two images
- **THEN** both files SHALL be in the run's working directory and the brief SHALL name their paths in the order sent

#### Scenario: Run moves to another node
- **WHEN** a run of a task with an attached screenshot dies and the retry is claimed by another node
- **THEN** the retry SHALL have the screenshot in its working directory, downloaded from Core
