# Contributing

By participating in this project, you agree to abide our
[code of conduct](https://github.com/k0rdent/community/blob/main/CODE_OF_CONDUCT.md).

## Set up your machine

> NOTE: we do not currently support Windows development,
> thus the following is applicable for Linux and Darwin.

Prerequisites:

- [Make](https://www.gnu.org/software/make/manual/make.html)
- [Go](https://go.dev/doc/install)

To install the required CLI tools:

```bash
make cli-install
```

## Build

Clone `kcm` anywhere:

```sh
git clone https://github.com/k0rdent/kcm.git
```

`cd` into the directory and install the dependencies:

```bash
go mod tidy
```

Build then the binaries:

```bash
make build
```

## Test

When you are satisfied with the changes you have made, run linter, and
unit- and env-tests:

```bash
make lint test
```

Before you commit the changes, generate the code and templates.
Optionally, a base and a head commits to diff changes from can be set:

```bash
make generate-all
# or
make BASE_COMMIT=origin/main HEAD_COMMIT=HEAD generate-all
```

To run E2E tests, please refer to the corresponding section of the
[dev docs](./docs/dev.md#running-e2e-tests-locally).

## Create a commit

Commit messages should follow
the [Conventional Commits](https://www.conventionalcommits.org) convention.

## Submit a PR

Push your branch to your fork, and open a new pull request against the `main` branch.

Please make sure that the title of the PR also follows the
[Conventional Commits](https://www.conventionalcommits.org) convention.
