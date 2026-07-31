<?php

declare(strict_types=1);

namespace Com\Example\App;

use Com\Example\Greeting\Greeter;

class Program
{
    public static function main(): string
    {
        $g = new Greeter('world');
        $message = $g->greet();
        echo $message;

        return $message;
    }
}
