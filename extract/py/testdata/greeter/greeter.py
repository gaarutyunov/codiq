"""Greeter greets by name."""


class Greeter:
    """Greeter greets by name."""

    def __init__(self, name: str) -> None:
        self.name = name

    def greet(self) -> str:
        """greet returns this Greeter's greeting."""
        return "hello, " + self.name
