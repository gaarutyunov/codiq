module Com
  module Example
    module Greeting
      class Greeter
        attr_reader :name

        def initialize(name)
          @name = name
        end

        def greet
          "hello, #{@name}"
        end
      end
    end
  end
end
