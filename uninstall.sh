#!/bin/sh
set -eu

install_dir="$HOME/.local/bin"
install_exe="$install_dir/amonhen"
shell_name=$(basename "${SHELL:-sh}")
path_file=
path_line='export PATH="$HOME/.local/bin:$PATH"'

case "$shell_name" in
	zsh) path_file="$HOME/.zshrc" ;;
	bash) path_file="$HOME/.bashrc" ;;
	fish)
		path_file="$HOME/.config/fish/config.fish"
		path_line='fish_add_path "$HOME/.local/bin"'
		;;
	*) path_file="$HOME/.profile" ;;
esac

if [ -f "$path_file" ]; then
	tmp_file="$path_file.amonhen-$$"
	trap 'rm -f "$tmp_file"' 0
	awk -v marker='# Added by Amon Hen installer' -v expected="$path_line" '
		pending && $0 == expected { pending = 0; next }
		pending { print marker; pending = 0 }
		$0 == marker { pending = 1; next }
		{ print }
		END { if (pending) print marker }
	' "$path_file" > "$tmp_file"
	mv "$tmp_file" "$path_file"
fi

if [ -f "$install_exe" ]; then
	rm -f "$install_exe"
fi
rmdir "$install_dir" 2>/dev/null || true

echo 'Removed Amon Hen and its PATH entry. Project settings and OS credentials were left intact.'
