<?php

declare(strict_types=1);

namespace Com\Example\Greeting;

/** Loudly is horizontal reuse and not an interface: it gives rather than demands. */
trait Loudly
{
    public function shout(): string
    {
        return strtoupper($this->greet());
    }
}

/** Greeter greets by name. */
class Greeter implements Speaker
{
    use Loudly;

    public const GREETING = 'hello';

    private string $name;

    /** Greeter builds a Greeter for name. */
    public function __construct(string $name)
    {
        $this->name = $name;
    }

    /** greet returns this Greeter's greeting. */
    public function greet(): string
    {
        return self::GREETING . ', ' . $this->name;
    }
}
