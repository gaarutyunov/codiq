using Com.Example.Greeting;

namespace Com.Example.App;

public static class Program
{
    public static void Main(string[] args)
    {
        Greeter g = new Greeter("world");
        string message = g.Greet();
        System.Console.WriteLine(message);
    }
}
