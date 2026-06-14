# Contributing to opus

Thank you for considering contributing to this project! Your contributions help make it better for everyone.

This document outlines guidelines for contributing, including how to report bugs, suggest features, and submit code changes.

## Code of Conduct

Please note that this project is released with a [Contributor Code of Conduct](CODE_OF_CONDUCT.md). By participating in this project, you agree to abide by its terms.

## How Can I Contribute?

### Reporting Bugs

If you find a bug, please open an issue on GitHub. Before opening a new issue, please check if a similar issue already exists. When reporting a bug, please include:

- A clear and concise description of the bug.
- Steps to reproduce the behavior.
- Expected behavior.
- Error messages or logs, if applicable.
- Your Go version (`go version`) and operating system.

> If the reproduction steps are unclear, please provide as much information as possible.

### Suggesting Enhancements

We welcome suggestions for new features or improvements to existing ones. Please open an issue on GitHub and describe your idea.

### Code Contributions

We appreciate code contributions! Please follow these steps:

1. **Fork the Repository:** Fork the repository to your GitHub account.
2. **Clone Your Fork:**
   ```bash
   git clone https://github.com/darui3018823/opus.git
   cd opus
   ```
3. **Set Up Your Development Environment:**
   - **Go:** Ensure you have Go 1.24 or newer installed.
   - **Dependencies:** Install Go modules.
     ```bash
     go mod tidy
     ```
4. **Create a New Branch:**
   ```bash
   git checkout -b feature/your-feature-name
   # or
   git checkout -b fix/your-bug-fix-name
   ```
5. **Make Your Changes:** Implement your changes, ensuring they adhere to the existing code style.
   - **Formatting:** Use `gofmt` to format your Go code.
     ```bash
     gofmt -w .
     ```
   - **Vetting:**
     ```bash
     go vet ./...
     ```
   - **Testing:** Write tests for new features or bug fixes, and make sure all existing tests pass.
     ```bash
     go test ./...
     go test -race ./...
     ```
6. **Commit Your Changes:** Follow [Conventional Commits](https://www.conventionalcommits.org/).
   - Examples:
     - `feat(celt): add band energy normalization`
     - `fix(silk): correct LPC filter boundary`
     - `docs: update CONTRIBUTING.md`
7. **Push Your Branch:**
   ```bash
   git push origin feature/your-feature-name
   ```
8. **Create a Pull Request:** Open a PR against the `main` branch.
   - Provide a clear title and description of your changes.
   - Reference any related issues (e.g., `Closes #42`).

### Documentation Contributions

Improvements to documentation are always welcome — READMEs, code comments, or any other explanatory text.

## Questions?

If you have any questions, feel free to open an issue on GitHub.
