package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/ksarpe/redis-golang/database"
	radix "github.com/mediocregopher/radix/v4"
)


func ResolveURL(c *fiber.Ctx) error{
	url := c.Params("url")

	r := database.RadixV4ClientsProducer{}

	rClient, err := r.NewClient("db:6379")
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "short not found in the database or cannot connect to DB",
		})
	}
	defer rClient.Close()

	var result string
	err = rClient.Do(radix.Cmd(&result, "GET", url))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "short not found in the database or cannot connect to DB",
		})
	}

	return c.Redirect(result, 301)

}