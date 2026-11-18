package com.example.rca;

class RcaResourceTest {
    void managerWiring() {
        boolean enabled = flags.enabled("TR_RCA_EVALUATION_API");
        assert enabled;
    }

    void validatesInput() {
        RcaRequest request = new RcaRequest();
        assert request != null;
    }
}
