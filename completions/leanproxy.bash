#!/bin/bash
# LeanProxy Bash Completion

_leanproxy() {
    local cur prev
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        serve)
            _arguments_help=(
                "--listen[Address to listen on]"
                "--upstream[Upstream JSON-RPC server URL]"
                "--config[Path to config file]"
                "--dry-run[Preview actions without making changes]"
                "-v[Enable verbose logging]"
                "--log-level[Log level: debug, info, warn, error]"
                "--help[Show help]"
            )
            COMPREPLY+=($(compgen -W "${_arguments_help[*]}" -- "$cur"))
            ;;
        version)
            COMPREPLY=()
            ;;
        completion)
            COMPREPLY+=($(compgen -W "bash zsh" -- "$cur"))
            ;;
        config)
            COMPREPLY+=($(compgen -W "init validate" -- "$cur"))
            ;;
        migrate)
            _migrate_opts=(
                "--format[Migration format: opencode, cursor, claude, vscode]"
                "--dry-run[Preview migration without making changes]"
            )
            COMPREPLY+=($(compgen -W "${_migrate_opts[*]}" -- "$cur"))
            ;;
        *)
            COMPREPLY+=($(compgen -W "serve version completion config init migrate" -- "$cur"))
            ;;
    esac
}

_leanproxy "$@"