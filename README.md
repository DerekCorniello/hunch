# hunch

Hunch is a shell companion that learns your command-line behavior and predicts what you’re most likely to do next.

It builds a lightweight model from your own command history and uses it to suggest the next command after every execution.

No AI. No cloud. Just your habits turned into fast, local predictions.

---

## What it does (end goal)

After you run a command like:

```bash
mkdir project
````

Hunch learns patterns such as:

```text
mkdir DIR → cd DIR
git clone REPO → cd REPO
cargo build → cargo run
```

Then it suggests:

```bash
cd project
```

or similar likely next steps.

### Core behavior

* Observes executed shell commands
* Normalizes them into templates (e.g. `mkdir foo → mkdir DIR`)
* Builds transition weights between command patterns
* Predicts next likely command
* Suggests it instantly in the terminal

---

## Architecture

* **core/**
  Learning + prediction logic (graph, normalization, scoring)

* **daemon/**
  Background process

  * stores data in SQLite
  * receives shell events
  * requests predictions from core

* **cli/**
  Debug + inspection tool

  * stats
  * reset
  * export/import
  * diagnostics

* **integrations/**
  Shell hooks (zsh, bash, fish)

  * capture commands
  * send to daemon
  * display suggestions

---

## UX vision

Eventually Hunch will feel like:

```bash
❯ mkdir project
❯ cd project   # (ghost suggestion appears)
```

or:

```text
💡 hunch: cd project
```

The user never explicitly "asks" for suggestions. They just appear as part of normal shell flow.

---

## Current state

This project is currently in early scaffolding.

Right now it contains:

* Basic planned directory structure
* Initial design for:

  * command normalization
  * transition graph model
  * daemon + shell integration split
* No working implementation yet
* No prediction engine yet
* No shell hooks yet

In short:

> Architecture and design phase — implementation has not started.

---

## Non-goals (for now)

* No cloud sync
* No AI/LLM usage
* No distributed system
* No complex shell parsing
* No multi-user modeling

---

## Philosophy

Hunch is intentionally simple:

* Learn from repetition
* Generalize command patterns
* Predict next actions
* Stay fast and local

If it feels “obvious” in hindsight, it belongs in Hunch.

