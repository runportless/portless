package dev.portless.store.inventory;

import java.util.List;

import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;

@RestController
@RequestMapping("/inventory")
public class InventoryController {
    private final InventoryCatalog catalog;

    public InventoryController(InventoryCatalog catalog) {
        this.catalog = catalog;
    }

    @GetMapping
    public List<InventoryItem> catalog() {
        return catalog.items();
    }

    @GetMapping("/{sku}")
    public ResponseEntity<InventoryAvailability> availability(
            @PathVariable String sku,
            @RequestParam(defaultValue = "1") int quantity) {
        if (quantity < 1) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "quantity must be positive");
        }
        return catalog.availability(sku, quantity)
                .map(ResponseEntity::ok)
                .orElseGet(() -> ResponseEntity.notFound().build());
    }

    @PostMapping("/{sku}/reservations")
    public ResponseEntity<?> reserve(
            @PathVariable String sku,
            @RequestBody InventoryReservationRequest request) {
        if (request.quantity() < 1 || request.quantity() > 100) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "quantity must be between 1 and 100");
        }
        InventoryReservationOutcome outcome = catalog.reserve(sku, request.quantity());
        return switch (outcome.status()) {
            case RESERVED -> ResponseEntity.status(HttpStatus.CREATED).body(outcome.result());
            case INSUFFICIENT -> ResponseEntity.status(HttpStatus.CONFLICT).body(outcome.availability());
            case NOT_FOUND -> ResponseEntity.notFound().build();
        };
    }

    @PostMapping("/reservations/{reservationID}/release")
    public ResponseEntity<?> release(@PathVariable long reservationID) {
        if (!catalog.release(reservationID)) {
            return ResponseEntity.notFound().build();
        }
        return ResponseEntity.ok(new InventoryReservationState(reservationID, "released"));
    }
}
