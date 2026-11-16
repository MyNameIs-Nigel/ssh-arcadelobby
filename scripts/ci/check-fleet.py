"""Compare production registry endpoints with rendered Compose services."""

import json
from pathlib import Path
import sys
import tomllib


def main(compose_path, registry_path):
    services = json.loads(Path(compose_path).read_text())["services"]
    registry = tomllib.loads(Path(registry_path).read_text())
    router_networks = services["router"]["networks"]

    for game in registry["games"]:
        host, separator, port = game["addr"].partition(":")
        service = services.get(host)
        if not separator or not service or host in {"router", "web"}:
            sys.exit(f"{game['id']}: {game['addr']} must name a Compose game service and port")
        if not router_networks.keys() & service["networks"].keys():
            sys.exit(f"{game['id']}: {host} has no network in common with the router")
        listen_port = service["environment"].get(f"{host.upper()}_LISTEN_PORT")
        if port != listen_port:
            sys.exit(f"{game['id']}: registry port {port} differs from {host} port {listen_port}")

    print(services["web"]["image"])


if __name__ == "__main__":
    main(*sys.argv[1:])
