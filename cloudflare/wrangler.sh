#!/bin/sh
# Runs Wrangler with its own login/config home so this personal project never
# uses or overwrites the Cloudflare login of other projects on this machine.
# First time: ./wrangler.sh login
export XDG_CONFIG_HOME="${N9N_WRANGLER_HOME:-$HOME/.config/n9n-cf}"
exec npx wrangler "$@"
