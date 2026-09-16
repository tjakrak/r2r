# B5 RIE802 inputs for the Excel/VBA-to-R2R prompt

Use [excel_macro_to_r2r_model_prompt.md](excel_macro_to_r2r_model_prompt.md) as the complete prompt.
Replace its **Inputs provided** section with the filled values below, then attach the workbook and
exported VBA source.

Do not provide the AI with a prewritten formula or recipe table. It must derive those from the Excel
workbook and macro source, following the analysis, clarification, validation, and output rules in the
generic prompt.

## Inputs provided

- Excel workbook: `B5_RIE802_Model_Development.xlsm`
- Exported VBA/macro source: exported VBA source containing `B5_RIE802_Mode`
- Target macro/procedure: `B5_RIE802_Mode`
- SQL engine: `sqlserver`
- Configured R2R data-source identifier: `wf8parametric`
- Values supplied by the external application:
  - `wafer_id`: `string` — wafer identifier supplied for each run
  - `product`: `string` — product number supplied for each run
- Required routing indexes:
  - `machine`: `B5_RIE802`
  - `action`: `RECIPE_SELECTION`
  - `operation`: `BPI190`, `CMS190`, or `CLU200`
  - `material`: `WAFER`
- Maximum unfinished runs per execution partition: `1`
- Unfinished-run partition fields: `wafer_id`
- Desired output parameter(s): `recipe` with data type `string`
- Known database tables and columns:
  - Database: `WF8PARAMETRIC`
  - Table: `dbo.WF8_OPC_LOT_DATA_HIST`
  - `ContainerName`: wafer identifier
  - `Operation`: historical operation identifier
  - `WIPDataName`: measurement name
  - `WIPDATAVALUE`: measurement value
  - `MoveInTimestamp`: history timestamp
- Additional business clarification:
  - The external application supplies `operation` as a routing index and supplies `wafer_id` and
    `product` as external inputs; do not query them.
  - Use the workbook and VBA as the source of truth for supported operations, product mappings,
    calculations, thresholds, recipe strings, comparison boundaries, and missing-data behavior.
  - If the workbook and VBA conflict or a database detail is insufficient, ask clarification questions
    instead of inventing behavior.
- Output mode: `REVIEW_AND_JSON`

## Attachments

Attach both of these to the AI conversation:

1. `B5_RIE802_Model_Development.xlsm`
2. The exported VBA module containing `B5_RIE802_Mode` as `.bas`, `.cls`, or plain text

If the workbook has been split into a macro-free workbook and a separate macro text file, replace the
two filenames above with the actual attachment names. Do not change the remaining input values unless
the deployment configuration has changed.
