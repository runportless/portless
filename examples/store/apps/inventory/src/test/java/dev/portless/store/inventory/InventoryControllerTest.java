package dev.portless.store.inventory;

import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import java.time.Instant;
import java.util.Optional;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.http.MediaType;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

@WebMvcTest(InventoryController.class)
class InventoryControllerTest {
    @Autowired
    private MockMvc mvc;

    @MockitoBean
    private InventoryCatalog catalog;

    @Test
    void reportsWhetherTheRequestedQuantityIsAvailable() throws Exception {
        when(catalog.availability("coffee-mug", 2)).thenReturn(Optional.of(
                new InventoryAvailability("coffee-mug", "Ceramic Coffee Mug", 2, 24, true, "ord-01")));

        mvc.perform(get("/inventory/coffee-mug").queryParam("quantity", "2"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.sku").value("coffee-mug"))
                .andExpect(jsonPath("$.requested").value(2))
                .andExpect(jsonPath("$.onHand").value(24))
                .andExpect(jsonPath("$.available").value(true));
    }

    @Test
    void returnsNotFoundForAnUnknownSku() throws Exception {
        when(catalog.availability("unknown", 1)).thenReturn(Optional.empty());

        mvc.perform(get("/inventory/unknown"))
                .andExpect(status().isNotFound());
    }

    @Test
    void reservesStockAndReportsThePersistedRemainder() throws Exception {
        InventoryReservation reservation = new InventoryReservation(
                17, "coffee-mug", 2, "reserved", Instant.parse("2026-08-25T18:00:00Z"));
        when(catalog.reserve("coffee-mug", 2)).thenReturn(InventoryReservationOutcome.reserved(
                new InventoryReservationResult(
                        reservation,
                        new InventoryItem("coffee-mug", "Ceramic Coffee Mug", 22, "ord-01"))));

        mvc.perform(post("/inventory/coffee-mug/reservations")
                        .contentType(MediaType.APPLICATION_JSON)
                        .content("{\"quantity\":2}"))
                .andExpect(status().isCreated())
                .andExpect(jsonPath("$.reservation.id").value(17))
                .andExpect(jsonPath("$.reservation.state").value("reserved"))
                .andExpect(jsonPath("$.inventory.onHand").value(22));
    }

    @Test
    void rejectsAReservationWhenPersistedStockIsInsufficient() throws Exception {
        when(catalog.reserve("usb-c-cable", 1)).thenReturn(InventoryReservationOutcome.insufficient(
                new InventoryAvailability("usb-c-cable", "USB-C Cable", 1, 0, false, "dfw-02")));

        mvc.perform(post("/inventory/usb-c-cable/reservations")
                        .contentType(MediaType.APPLICATION_JSON)
                        .content("{\"quantity\":1}"))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.available").value(false))
                .andExpect(jsonPath("$.onHand").value(0));
    }

    @Test
    void releasesAReservationIdempotently() throws Exception {
        when(catalog.release(17)).thenReturn(true);

        mvc.perform(post("/inventory/reservations/17/release"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.id").value(17))
                .andExpect(jsonPath("$.state").value("released"));
    }
}
