require "greeter/greeter"

module Com
  module Example
    module App
      class Program
        def self.main
          g = Com::Example::Greeting::Greeter.new("world")
          message = g.greet
          puts message
          message
        end
      end
    end
  end
end
