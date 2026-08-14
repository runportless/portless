package dev.portless.store.inventory;

import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.context.annotation.Import;
import org.springframework.test.web.servlet.MockMvc;

@WebMvcTest(InventoryController.class)
@Import(InventoryCatalog.class)
class InventoryControllerTest {
    @Autowired
    private MockMvc mvc;

    @Test
    void reportsWhetherTheRequestedQuantityIsAvailable() throws Exception {
        mvc.perform(get("/inventory/coffee-mug").queryParam("quantity", "2"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.sku").value("coffee-mug"))
                .andExpect(jsonPath("$.requested").value(2))
                .andExpect(jsonPath("$.onHand").value(24))
                .andExpect(jsonPath("$.available").value(true));
    }

    @Test
    void returnsNotFoundForAnUnknownSku() throws Exception {
        mvc.perform(get("/inventory/unknown"))
                .andExpect(status().isNotFound());
    }
}
