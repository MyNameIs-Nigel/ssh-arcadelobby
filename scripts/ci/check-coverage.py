"""Enforce statement coverage floors for the lobby's critical packages."""

from pathlib import Path
import sys


FLOORS = {
    "internal/bridge": 79,
    "internal/lobby": 85,
    "internal/registry": 92,
    "internal/server": 79,
}


def main(profile):
    covered = dict.fromkeys(FLOORS, 0)
    total = dict.fromkeys(FLOORS, 0)

    with Path(profile).open() as coverage:
        next(coverage)
        for line in coverage:
            location, statements, count = line.split()
            source_file = location.rsplit(":", 1)[0]
            _, _, package = source_file.rpartition("/internal/")
            package = "internal/" + package.rsplit("/", 1)[0]
            if package not in FLOORS:
                continue
            total[package] += int(statements)
            if int(count) > 0:
                covered[package] += int(statements)

    passed = True
    for package, minimum in FLOORS.items():
        if not total[package]:
            print(f"{package}: missing from coverage profile", file=sys.stderr)
            passed = False
            continue
        percent = 100 * covered[package] / total[package]
        print(f"{package}: {percent:.1f}% (minimum {minimum}%)")
        passed &= percent >= minimum

    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1]))
