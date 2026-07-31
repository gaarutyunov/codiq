namespace Com.Example.Greeting;

/// <summary>Greeter greets by name.</summary>
public class Greeter : ISpeaker
{
    private readonly string _name;

    /// <summary>Greeter builds a Greeter for name.</summary>
    public Greeter(string name)
    {
        _name = name;
    }

    /// <summary>Greet returns this Greeter's greeting.</summary>
    public string Greet()
    {
        return "hello, " + _name;
    }
}
