"""Module docstring."""


def top_level_function(x: int) -> int:
    """Return x doubled."""
    return x * 2


class Greeter:
    """A friendly greeter."""

    def __init__(self, name: str) -> None:
        """Store the name."""
        self.name = name

    @staticmethod
    def shout(message: str) -> str:
        """Shout a message."""
        return message.upper()


@deprecated
def legacy_function() -> None:
    pass


def single_quote_docstring_function() -> str:
    'Uses single quotes.'
    return "ok"


def double_quote_docstring_function() -> str:
    "Uses double quotes."
    return "ok"


def triple_single_quote_docstring_function() -> str:
    '''Uses triple single quotes.'''
    return "ok"


def non_string_first_statement_function() -> None:
    x = 1
    return None
