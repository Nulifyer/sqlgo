# Working agreements

- Keep the TUI accessible and responsive while asynchronous work runs. Keep
  navigation and editing available, move slow I/O and expensive analysis off the
  event loop, show progress in text, and check document identity and revision
  before applying background results.
