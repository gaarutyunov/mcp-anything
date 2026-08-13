return {
  name = "mcp-anything",
  fields = {
    { config = {
        type = "record",
        fields = {
          { config_path = {
              type = "string",
              required = false,
          }},
        },
    }},
  },
}
