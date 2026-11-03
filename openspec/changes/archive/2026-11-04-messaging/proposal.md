# Messaging, owner questions and inbox

Add durable asynchronous agent/owner messages, structured questions and a CLI/HTTP owner inbox.
Blocking questions suspend tasks only after terminal runtime evidence, freeing the worker; explicit
answers resume a separate run on the same task thread. Questions to agents create separate
communication runs; owner-manager conversations keep a stable logical session. Core remains the
sole orchestrator and state owner. Approval grants and Decision Registry stay separate stages.
