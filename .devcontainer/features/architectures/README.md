# Architectures

## Default Choices

| Project Type | Default Architecture |
|----------------|------------------------|
| API/Scalable Backend | **Sliceable Monolith** |
| Web scripting (PHP, Ruby) | **MVC** |
| CLI/Tools | **Flat** |
| Library/Package | **Package** |
| Mobile (Flutter) | **MVVM** |

## Architecture List

| Architecture | File | Use Case |
|--------------|---------|-------------|
| MVC | `mvc.md` | Web apps, PHP, Ruby, Django |
| MVP | `mvp.md` | Desktop, Android legacy |
| MVVM | `mvvm.md` | Mobile, Reactive Frontend |
| Layered | `layered.md` | Traditional apps |
| Clean | `clean.md` | Complex apps, testability |
| Hexagonal | `hexagonal.md` | Domain-centric, ports & adapters |
| Onion | `onion.md` | Enterprise, .NET |
| DDD | `ddd.md` | Complex domains |
| Microservices | `microservices.md` | Large team, independent scaling |
| Sliceable Monolith | `sliceable-monolith.md` | **Recommended** - Best of both worlds |
| Event-Driven | `event-driven.md` | Async, strong decoupling |
| Serverless | `serverless.md` | Functions, pay-per-use |
| Flat | `flat.md` | Scripts, CLI, POC |
| Package | `package.md` | Reusable libraries |
